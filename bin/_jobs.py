"""Disposable acquisition checkpoints. All operations run under the cache acquisition lock."""

import json
import re
from datetime import datetime, timedelta, timezone
from email.utils import parsedate_to_datetime

from _evidence import artifact_ref, canonical, digest, fields, natural, repository as repository_identity, same_item, same_repository, timestamp, validate_item, validate_ref, validate_revision, version
from _storage import atomic_writer, sync_directory

COLLECTIONS = {"comments", "files", "reviews", "review_comments", "timeline"}


def sealed(value):
    return dict(value, checksum=digest(canonical(value)))


def read_record(path, artifact, required, optional=()):
    value = json.loads(path.read_text(encoding="utf-8"))
    fields(value, ("schema_version", "artifact", "checksum", *required), optional)
    version(value, artifact)
    if value["checksum"] != digest(canonical({key: item for key, item in value.items() if key != "checksum"})):
        raise ValueError(f"{artifact} checksum mismatch; preserve and inspect the job file before retrying")

    return value


def write_record(cache, path, value):
    path.parent.mkdir(exist_ok=True)
    sync_directory(cache.root)
    with atomic_writer(path) as out:
        out.write(canonical(sealed(value)) + "\n")


def page_keys(name, rows):
    if not isinstance(rows, list) or len(rows) > 100 or any(not isinstance(row, dict) for row in rows):
        raise ValueError("collection page must be an array of at most 100 objects")

    if name == "timeline":
        keys = [timeline_key(row) for row in rows]
        if len(set(keys)) != len(keys):
            raise ValueError("repeated timeline identity; pagination may have shifted")

        return keys

    keys = [row.get("filename") if name == "files" else row.get("id") for row in rows]
    for key in keys:
        valid = isinstance(key, str) and bool(key) if name == "files" else type(key) is int and key > 0
        if not valid:
            raise ValueError("missing collection identity")

    if len(set(keys)) != len(keys):
        raise ValueError("repeated collection identity; pagination may have shifted")

    return keys


def timeline_key(row):
    event = row.get("event")
    if not isinstance(event, str) or not event:
        raise ValueError("missing timeline event type")

    if type(row.get("id")) is int and row["id"] > 0:
        return f"{event}:id:{row['id']}"

    if isinstance(row.get("node_id"), str) and row["node_id"]:
        return f"{event}:node:{row['node_id']}"

    if event == "committed" and isinstance(row.get("sha"), str) and re.fullmatch(r"[0-9a-f]{40,64}", row["sha"]):
        return f"{event}:sha:{row['sha']}"

    raise ValueError("missing stable timeline identity; coverage is incomplete")


def valid_etag(value):
    return value is None or (isinstance(value, str) and 0 < len(value) <= 4096 and all(32 <= ord(char) < 127 for char in value))


class PageJob:
    def __init__(self, cache, repository, identity, name, resource, revision, expected, scope):
        validate_item(identity)
        if name not in COLLECTIONS:
            raise ValueError("unsupported page job component")

        if identity["kind"] == "issue" and name not in ("comments", "timeline"):
            raise ValueError("PR-only page job on an issue")

        self.cache = cache
        self.path = cache.path("jobs", f"{identity['kind']}-{identity['number']}-{name}.json")
        self.binding = dict(repository=repository, identity=identity, component=name, resource=resource, revision=revision, expected_count=expected, scope=scope)
        self.pages = []
        self.state = "collecting"

    def load(self, mode, max_age, now):
        if not self.path.exists():
            return []

        value = read_record(self.path, "evidence-page-job", (*self.binding, "state", "pages"))
        same_repository(self.binding["repository"], value["repository"])
        same_item(self.binding["identity"], value["identity"])
        validate_revision(value["revision"])
        if value["expected_count"] is not None:
            natural(value["expected_count"], "job expected_count")

        if value["component"] != self.binding["component"] or value["resource"] != self.binding["resource"]:
            raise ValueError("page job endpoint/component mismatch")

        scope = value["scope"]
        if not isinstance(scope, list) or len(scope) != 2 or any(not isinstance(side, list) or len(side) != 3 for side in scope):
            raise ValueError("invalid job repository scope")

        for full_name, database_id, node_id in scope:
            repository_identity("unknown/unknown" if full_name is None else full_name, database_id=database_id, node_id=node_id)

        if value["state"] not in ("collecting", "complete", "invalidated") or not isinstance(value["pages"], list):
            raise ValueError("invalid page job state/pages")

        pages, seen = [], set()
        for position, page in enumerate(value["pages"], 1):
            fields(page, ("number", "object", "more", "etag", "fetched_at"))
            if type(page["number"]) is not int or page["number"] != position or type(page["more"]) is not bool or not valid_etag(page["etag"]):
                raise ValueError("invalid page checkpoint/cursor/header")

            if position < len(value["pages"]) and not page["more"]:
                raise ValueError("page follows a terminal checkpoint")

            timestamp(page["fetched_at"])
            validate_ref(page["object"])
            if page["object"]["format"] != "json":
                raise ValueError("page checkpoint must reference JSON")

            rows = json.loads(self.cache.read_object(page["object"]))
            keys = set(page_keys(value["component"], rows))
            if seen & keys:
                raise ValueError("repeated identity across checkpoint pages")

            seen.update(keys)
            pages.append((page, rows))

        if value["state"] == "complete" and (not pages or pages[-1][0]["more"] or (value["expected_count"] is not None and len(seen) != value["expected_count"])):
            raise ValueError("complete job has incomplete pagination/counts")

        compatible = value["revision"] == self.binding["revision"] and value["expected_count"] == self.binding["expected_count"] and value["scope"] == self.binding["scope"]
        fresh = all(0 <= (timestamp(now) - timestamp(page["fetched_at"])).total_seconds() <= max_age for page, _ in pages)
        if mode == "refresh" or value["state"] not in (("collecting", "complete") if mode == "poll-resume" else ("collecting",)) or not compatible or not fresh:
            return []

        self.pages = [page for page, _ in pages]

        return pages

    def save(self):
        write_record(self.cache, self.path, dict(schema_version=1, artifact="evidence-page-job", **self.binding, state=self.state, pages=self.pages))

    def reset(self):
        self.pages = []
        self.state = "collecting"
        self.save()

    def append(self, rows, more, etag, now):
        payload = canonical(rows) + "\n"
        ref = artifact_ref(payload)
        self.cache.store_object(ref, payload)

        self.pages.append(dict(number=len(self.pages) + 1, object=ref, more=more, etag=etag if valid_etag(etag) else None, fetched_at=now))
        self.save()

    def finish(self, state):
        self.state = state
        self.save()


class Cooldown:
    def __init__(self, cache):
        self.cache = cache
        self.path = cache.path("jobs", "cooldown.json")

    def read(self):
        if not self.path.exists():
            return None

        value = read_record(self.path, "evidence-cooldown", ("repository", "observed_at", "retry_at", "attempts", "reason"))
        same_repository(value["repository"], self.cache.require_ready()["repository"])
        natural(value["attempts"], "cooldown attempts", 1)
        if value["reason"] not in ("http-throttle-or-forbidden", "http-exhausted", "graphql-exhausted") or timestamp(value["retry_at"]) < timestamp(value["observed_at"]):
            raise ValueError("invalid cooldown reason/interval")

        return value

    def blocked(self, now):
        value = self.read()
        if value and timestamp(now) < timestamp(value["retry_at"]):
            return f"acquisition cooldown until {value['retry_at']} ({value['reason']}); no GitHub request made"

        return None

    def record(self, reason, now, headers=None, reset_at=None, same_request=False):
        headers = headers or {}
        current = timestamp(now)
        previous = self.read()
        attempts = previous["attempts"] + (0 if same_request else 1) if previous and current <= timestamp(previous["retry_at"]) + timedelta(hours=1) else 1
        deadlines = [current + timedelta(seconds=min(3600, 60 * 2 ** min(attempts - 1, 6)))]
        if previous:
            deadlines.append(timestamp(previous["retry_at"]))

        retry = headers.get("retry-after", "")
        try:
            deadlines.append(current + timedelta(seconds=int(retry)) if retry.isdigit() else parsedate_to_datetime(retry))
        except (TypeError, ValueError, OverflowError):
            pass

        if headers.get("x-ratelimit-remaining") == "0":
            try:
                deadlines.append(datetime.fromtimestamp(int(headers.get("x-ratelimit-reset", "")), timezone.utc))
            except (ValueError, OverflowError, OSError):
                pass

        if reset_at is not None:
            try:
                deadlines.append(timestamp(reset_at))
            except (ValueError, TypeError):
                pass

        deadline = max(value for value in deadlines if value.tzinfo is not None)
        write_record(self.cache, self.path, dict(schema_version=1, artifact="evidence-cooldown", repository=self.cache.require_ready()["repository"], observed_at=now, retry_at=deadline.isoformat(), attempts=attempts, reason=reason))


def inspect_jobs(cache):
    metadata = cache.metadata()
    if metadata is None:
        return dict(repository=cache.identity, jobs=[], cooldown=None)

    jobs = []
    for path in sorted(cache.path("jobs").glob("*-*-*.json")):
        # Full contract validation is performed by PageJob.load, using a locally constructed endpoint rather than a saved URL.
        value = json.loads(cache.path("jobs", path.name).read_text(encoding="utf-8"))
        identity, name = value["identity"], value["component"]
        validate_item(identity)
        base = f"repos/{metadata['repository']['full_name']}"
        resource = f"{base}/issues/{identity['number']}/{name}" if name in ("comments", "timeline") else f"{base}/pulls/{identity['number']}/{'comments' if name == 'review_comments' else name}"
        job = PageJob(cache, metadata["repository"], identity, name, resource, value["revision"], value["expected_count"], value["scope"])
        if path != job.path:
            raise ValueError("job filename/identity mismatch")

        job.load("refresh", 0, datetime.now(timezone.utc).isoformat())
        jobs.append(dict(identity=identity, component=name, state=value["state"], pages=len(value["pages"]), revision=value["revision"]))

    return dict(repository=metadata["repository"], jobs=jobs, cooldown=Cooldown(cache).read())
