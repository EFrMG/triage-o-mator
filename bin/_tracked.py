"""Local comment subscriptions for issues and PRs. A refresh checks them; opening an item changes nothing."""

import json

from _acquire import GitHubReader, PROFILES, ReadFailure, acquire, now
from _evidence import canonical, component_problems, digest, natural, same_item, same_repository, validate_item
from _jobs import Cooldown, read_record
from _storage import atomic_writer, local_dir, locked
from _triage import DATA_DIR

FIELDS = ("repository", "items")


def components_for(kind):
    return PROFILES["discussion"] + (["review_comments"] if kind == "pr" else [])


def path_for(cache):
    return DATA_DIR / "local" / "tracked-items.json"


def prepare_path(cache):
    local_dir(DATA_DIR / "tracked-items.json")
    return path_for(cache)


def save(path, value):
    value = {key: item for key, item in value.items() if key != "checksum"}
    value["checksum"] = digest(canonical(value))
    with atomic_writer(path) as out:
        out.write(canonical(value) + "\n")


def load(cache):
    path = path_for(cache)
    if not path.exists():
        return dict(schema_version=1, artifact="tracked-comments", repository=cache.identity, items=[])

    value = read_record(path, "tracked-comments", FIELDS)
    same_repository(cache.identity, value["repository"])
    seen = set()
    for item in value["items"]:
        if set(item) != {"identity", "title", "added_at", "checked_at", "comment_ids", "new_count", "error"}:
            raise ValueError("invalid tracked item fields")

        validate_item(item["identity"])
        key = (item["identity"]["kind"], item["identity"]["number"])
        if key in seen or item["identity"]["database_id"] is None or item["identity"]["node_id"] is None:
            raise ValueError("invalid or repeated tracked item identity")

        seen.add(key)
        if not isinstance(item["title"], str):
            raise ValueError("invalid tracked item title")

        from _evidence import timestamp

        timestamp(item["added_at"])
        timestamp(item["checked_at"])
        natural(item["new_count"], "new comment count")
        if not isinstance(item["comment_ids"], list) or any(not isinstance(value, str) or not value.startswith(("issue:", "review:")) or not value.split(":", 1)[1].isdigit() or int(value.split(":", 1)[1]) < 1 for value in item["comment_ids"]) or len(set(item["comment_ids"])) != len(item["comment_ids"]):
            raise ValueError("invalid tracked comment identities")

        if item["error"] is not None and (not isinstance(item["error"], str) or not item["error"]):
            raise ValueError("invalid tracked check error")

    return value


def checked(cache, manifest, kind, number):
    record = next((item for item in manifest["items"] if (item["identity"]["kind"], item["identity"]["number"]) == (kind, number)), None)
    if record is None:
        raise ValueError("tracked item is absent from its acquired snapshot")

    for name in components_for(kind):
        component = record["components"].get(name)
        if component is None or component_problems(name, component, record["revision"], manifest["completed_at"], 2**53):
            raise ValueError(f"tracked {name} is incomplete; previous comment count retained")

    summary = json.loads(cache.read_object(record["components"]["summary"]["object"]))
    comments = json.loads(cache.read_object(record["components"]["comments"]["object"]))
    ids = ["issue:" + str(comment.get("id")) for comment in comments]
    if kind == "pr":
        reviews = json.loads(cache.read_object(record["components"]["review_comments"]["object"]))
        ids += ["review:" + str(comment.get("id")) for comment in reviews]
    if any(not value.split(":", 1)[1].isdigit() or int(value.split(":", 1)[1]) < 1 for value in ids) or len(set(ids)) != len(ids):
        raise ValueError("tracked comments have invalid or repeated identities")

    return record["identity"], summary.get("title") or "", ids


def add(cache, kind, number, budget=20):
    validate_item(dict(kind=kind, number=number, database_id=None, node_id=None))
    natural(budget, "request budget", 1)
    cache.initialize()
    manifest, stats = acquire(cache, kind, number, components_for(kind), "refresh", 0, budget)
    identity, title, ids = checked(cache, manifest, kind, number)
    path = prepare_path(cache)
    with locked(path):
        value = load(cache)
        if any((item["identity"]["kind"], item["identity"]["number"]) == (kind, number) for item in value["items"]):
            raise ValueError("item is already tracked")

        stamp = now()
        value["items"].append(dict(identity=identity, title=title, added_at=stamp, checked_at=stamp,
                                   comment_ids=ids, new_count=0, error=None))
        value.pop("checksum", None)
        save(path, value)

    return dict(kind=kind, number=number, requests=stats["requests"], baseline_comments=len(ids))


def remove(cache, kind, number):
    path = prepare_path(cache)
    with locked(path):
        value = load(cache)
        kept = [item for item in value["items"] if (item["identity"]["kind"], item["identity"]["number"]) != (kind, number)]
        if len(kept) == len(value["items"]):
            raise ValueError("item is not tracked")

        value["items"] = kept
        value.pop("checksum", None)
        save(path, value)

    return dict(kind=kind, number=number, removed=True, evidence_retained=True)


def mark_read(cache, kind, number, checked_at, new_count):
    natural(new_count, "expected new comment count")
    path = prepare_path(cache)
    with locked(path):
        value = load(cache)
        row = next((item for item in value["items"] if (item["identity"]["kind"], item["identity"]["number"]) == (kind, number)), None)
        if row is None:
            raise ValueError("item is not tracked")
        if row["checked_at"] != checked_at or row["new_count"] != new_count:
            raise ValueError("tracked comments changed; reopen Notifications before marking read")

        row["new_count"] = 0
        value.pop("checksum", None)
        save(path, value)

    return dict(kind=kind, number=number, read=True)


def listing(cache, offset=0, limit=20):
    natural(offset, "offset")
    natural(limit, "limit", 1)
    if limit > 50:
        raise ValueError("tracked list limit exceeds 50")

    value = load(cache)
    rows = sorted(value["items"], key=lambda item: (item["new_count"] == 0, item["identity"]["kind"], item["identity"]["number"]))
    selected = [{key: item[key] for key in ("identity", "title", "checked_at", "new_count", "error")} for item in rows[offset:offset + limit]]
    return dict(repository=value["repository"], rows=selected, total=len(rows), unread_total=sum(item["new_count"] > 0 for item in rows), offset=offset,
                next=offset + limit if offset + limit < len(rows) else None, requests=0)


def check(cache, budget=100):
    natural(budget, "request budget", 1)
    value = load(cache)
    if not value["items"]:
        return dict(checked=0, failed=0, unread_total=0, requests=0, stopped=None)

    reader = GitHubReader(cache.identity["host"], budget, Cooldown(cache))
    updated = failed = 0
    for original in value["items"]:
        if reader.requests >= budget or reader.stopped:
            break

        identity = original["identity"]
        kind, number = identity["kind"], identity["number"]
        error = None
        try:
            manifest, _ = acquire(cache, kind, number, components_for(kind), "refresh", 0, budget, reader=reader)
            observed, title, ids = checked(cache, manifest, kind, number)
            same_item(identity, observed)
        except (ReadFailure, ValueError, OSError, KeyError, TypeError) as problem:
            error = str(problem)

        path = prepare_path(cache)
        with locked(path):
            current = load(cache)
            row = next((item for item in current["items"] if (item["identity"]["kind"], item["identity"]["number"]) == (kind, number)), None)
            if row is None or row["added_at"] != original["added_at"] or row["comment_ids"] != original["comment_ids"]:
                continue

            if error:
                row["error"] = error[:500]
                failed += 1
            else:
                row["new_count"] += len(set(ids) - set(row["comment_ids"]))
                row.update(title=title, checked_at=now(), comment_ids=ids, error=None)
                updated += 1

            current.pop("checksum", None)
            save(path, current)

    return dict(checked=updated, failed=failed, unread_total=listing(cache, 0, 1)["unread_total"], requests=reader.requests, stopped=reader.stopped or ("request budget exhausted" if reader.requests >= budget else None))
