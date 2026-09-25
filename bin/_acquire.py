"""Bounded, read-only REST acquisition for selected items. Source text never selects endpoints or grants authority."""

import copy
import json
import os
import re
import subprocess
from datetime import datetime, timezone

from _evidence import DEFAULT_MAX_AGE
from _evidence import PR_ONLY, artifact_ref, canonical, component_problems, natural, object_name, repository, same_item, same_repository, seal_snapshot, validate_item, validate_revision
from _storage import locked
from _jobs import Cooldown, PageJob, page_keys

PROFILES = {
    "backlog": ["summary", "comments", "files", "diff", "closing_issues"],
    "closure-watch": ["summary", "comments", "timeline"],
    "discussion": ["summary", "comments"],
    "pr-context": ["summary", "comments", "files", "reviews", "review_comments"],
    "pr-code": ["summary", "comments", "files", "diff"],
    "pr-comparison": ["summary", "comments", "files", "diff", "reviews", "review_comments", "checks", "closing_issues"],
}
MODES = ("offline", "cache-preferred", "refresh")

# This is the only GraphQL operation exposed by the transport. Source text is supplied only as variables, never query syntax.
CLOSING_QUERY = """query ClosingIssues($id: ID!, $after: String) {
  node(id: $id) {
    ... on PullRequest {
      id number url updatedAt baseRefOid headRefOid
      repository { id nameWithOwner }
      closingIssuesReferences(first: 100, after: $after) {
        totalCount pageInfo { hasNextPage endCursor }
        nodes { id number url state updatedAt repository { id nameWithOwner } }
      }
    }
  }
  rateLimit { remaining resetAt }
}"""


def now():
    return datetime.now(timezone.utc).isoformat()


class ReadFailure(Exception):
    def __init__(self, message, unavailable=False, http_status=None):
        super().__init__(message)
        self.unavailable = unavailable
        self.http_status = http_status


class GitHubReader:
    def __init__(self, host, budget=100, cooldown=None):
        natural(budget, "request budget", 1)
        self.host = host
        self.budget = budget
        self.requests = 0
        self.stopped = None
        self.cooldown = cooldown
        self.resumed_pages = 0
        self.throttled_request = None

    def get(self, resource, etag=None):
        body, headers = self._request(resource, etag=etag)
        return self._json(body) if body is not None else None, headers

    def throttle(self, reason, headers=None, reset_at=None):
        if self.cooldown:
            self.cooldown.record(reason, now(), headers, reset_at, same_request=self.throttled_request == self.requests)
            self.throttled_request = self.requests

    def diff(self, resource):
        return self._request(resource, accept="application/vnd.github.diff")

    def closing_page(self, node_id, after):
        body, _ = self._request("graphql", closing_variables=dict(id=node_id, after=after))
        result = self._json(body)
        if not isinstance(result, dict):
            raise ReadFailure("GraphQL response is not an object")

        data = result.get("data") or {}
        if not isinstance(data, dict):
            raise ReadFailure("GraphQL data is not an object")

        limit = data.get("rateLimit") or {}
        if not isinstance(limit, dict):
            raise ReadFailure("GraphQL rate limit is not an object")

        if limit.get("remaining") == 0:
            self.stopped = "GraphQL rate limit exhausted; check reset time before retrying"
            self.throttle("graphql-exhausted", reset_at=limit.get("resetAt"))

        if result.get("errors"):
            if isinstance(result["errors"], list) and any(isinstance(error, dict) and error.get("type") == "RATE_LIMITED" for error in result["errors"]):
                self.stopped = "GraphQL rate limit exhausted; check reset time before retrying"
                if limit.get("remaining") != 0:
                    self.throttle("graphql-exhausted", reset_at=limit.get("resetAt"))

            raise ReadFailure("GraphQL returned errors; this page is not complete evidence")

        return data.get("node")

    @staticmethod
    def _json(body):
        try:
            return json.loads(body)
        except ValueError as error:
            raise ReadFailure("GitHub returned invalid JSON") from error

    def _request(self, resource, accept="application/vnd.github+json", closing_variables=None, etag=None, graphql_query=None, graphql_variables=None):
        if self.stopped:
            raise ReadFailure(self.stopped)

        if self.cooldown:
            blocked = self.cooldown.blocked(now())
            if blocked:
                self.stopped = blocked
                raise ReadFailure(blocked)

        if self.requests >= self.budget:
            raise ReadFailure("request budget exhausted; rerun to resume completed components")

        self.requests += 1
        env = dict(os.environ)
        env.pop("GH_DEBUG", None)
        command = ["gh", "api", "--hostname", self.host, "--method", "GET", "--include", "--header", f"Accept: {accept}", "--header", "X-GitHub-Api-Version: 2022-11-28"]
        if etag is not None:
            from _jobs import valid_etag

            if not valid_etag(etag):
                raise ValueError("invalid saved ETag")

            command.extend(["--header", f"If-None-Match: {etag}"])

        payload = None
        if closing_variables is not None or graphql_query is not None:
            if resource != "graphql":
                raise ValueError("fixed GraphQL queries must use the GraphQL endpoint")

            command[command.index("GET")] = "POST"
            command.extend(["--input", "-"])
            payload = canonical(dict(query=graphql_query or CLOSING_QUERY, variables=graphql_variables if graphql_query is not None else closing_variables)).encode("utf-8")

        try:
            result = subprocess.run(
                command + [resource], input=payload, capture_output=True, timeout=45, env=env,
            )
            output = result.stdout.decode("utf-8")
        except (OSError, subprocess.TimeoutExpired, UnicodeError) as error:
            raise ReadFailure(f"GitHub read failed ({type(error).__name__})") from error

        parts = re.split(r"\r?\n\r?\n", output, maxsplit=1)
        header, body = parts if len(parts) == 2 else (output, "")
        lines = header.splitlines()
        match = re.fullmatch(r"HTTP/\S+ (\d{3})(?: .*)?", lines[0]) if lines else None
        if len(parts) != 2 or not match:
            raise ReadFailure("GitHub response has no valid HTTP status/headers")

        status = int(match[1])
        headers = {}
        for line in lines[1:]:
            key, sep, value = line.partition(":")
            if sep:
                headers[key.lower()] = value.strip()

        # Stop the entire run on throttling/permission failures. Never automatically retry before Retry-After or reset, and never persist gh stderr or credentials.
        if status in (403, 429):
            self.stopped = f"HTTP {status}; acquisition stopped (check permissions/rate limits before retrying)"
            for key in ("retry-after", "x-ratelimit-reset"):
                if headers.get(key, "").isdigit():
                    self.stopped += f"; {key}={headers[key]}"

            self.throttle("http-throttle-or-forbidden", headers)

        if status in (200, 304) and headers.get("x-ratelimit-remaining") == "0":
            self.stopped = "GitHub rate limit exhausted; check reset time before retrying"
            self.throttle("http-exhausted", headers)

        # gh reports >299 as a CLI error, including a valid conditional 304. Only accept a bodyless 304 for our own saved validator.
        headers["_triage_not_modified"] = False
        if status == 304 and etag is not None and not body.strip():
            headers["_triage_not_modified"] = True
            return None, headers

        # gh exits nonzero for GraphQL errors even with HTTP 200; let the fixed-query reader inspect them and stop on rate limiting.
        graphql_errors = False
        if result.returncode and status == 200 and (closing_variables is not None or graphql_query is not None):
            data = self._json(body)
            graphql_errors = isinstance(data, dict) and bool(data.get("errors"))

        if (result.returncode and not graphql_errors) or status != 200:
            raise ReadFailure(self.stopped or f"GitHub read returned HTTP {status}", unavailable=status in (404, 410), http_status=status)

        return body, headers


def summary_identity(data, kind, number, repo):
    if not isinstance(data, dict):
        raise ValueError("summary must be an object")

    identity = dict(kind=kind, number=data.get("number"), database_id=data.get("id"), node_id=data.get("node_id"))
    validate_item(identity)
    same_item(dict(kind=kind, number=number, database_id=None, node_id=None), identity)
    if identity["database_id"] is None or identity["node_id"] is None:
        raise ValueError("GitHub summary is missing stable item IDs")

    # A redirected issue number or a PR returned through the issue endpoint must not silently enter another namespace.
    expected_url = f"https://{repo['host']}/{repo['full_name']}/{'pull' if kind == 'pr' else 'issues'}/{number}"
    if data.get("html_url") != expected_url or (kind == "issue" and "pull_request" in data):
        raise ValueError("item URL/kind changed; reconcile the namespace explicitly")

    if data.get("state") not in ("open", "closed") or "body" not in data or (data["body"] is not None and not isinstance(data["body"], str)):
        raise ValueError("summary lacks observed state/body")

    revision = dict(updated_at=data.get("updated_at"), base_sha=None, head_sha=None)
    if kind == "pr":
        base = data.get("base") or {}
        head = data.get("head") or {}
        base_repo = base.get("repo") or {}
        same_repository(repo, repository(base_repo.get("full_name"), host=repo["host"], database_id=base_repo.get("id"), node_id=base_repo.get("node_id")))
        revision.update(base_sha=base.get("sha"), head_sha=head.get("sha"))
        if type(data.get("merged")) is not bool:
            raise ValueError("PR summary lacks an explicit merged state")

    validate_revision(revision)
    if revision["updated_at"] is None or (kind == "pr" and (revision["head_sha"] is None or revision["base_sha"] is None)):
        raise ValueError("summary lacks required observation revisions")

    return identity, revision


def descriptor(resource, revision, status="failed", error="not fetched yet"):
    return dict(status=status, fetched_at=now(), source=dict(transport="rest", resource=resource), revision=dict(revision), expected_count=None, received_count=None, pagination_complete=None, truncated=False, error=error, object=None)


def attach(component, data, payloads):
    payload = canonical(data) + "\n"
    ref = artifact_ref(payload)
    payloads[object_name(ref)] = payload
    component["object"] = ref


def check_scope(summary):
    """Stable repository scope matters even when the same commit appears in another fork."""
    return tuple(tuple(((summary.get(side) or {}).get("repo") or {}).get(key) for key in ("full_name", "id", "node_id")) for side in ("base", "head"))


def collect(reader, resource, name, revision, expected, payloads, job=None, mode="refresh", max_age=DEFAULT_MAX_AGE):
    component = descriptor(resource, revision)
    component.update(expected_count=expected, received_count=0, pagination_complete=False)
    values, seen = [], set()
    page = 1
    saved = job.load(mode, max_age, now()) if job else []
    if job and not saved:
        job.reset()

    try:
        if saved:
            # Mutable discussion/review pages must all be revalidated. Commit-bound files recheck the boundary; earlier pages remain bound to the verified repository/revision/count and age window.
            positions = [len(saved) - 1] if name == "files" else range(len(saved))
            matches = True
            for position in positions:
                checkpoint, rows = saved[position]
                # A terminal page is fetched unconditionally: its unchanged body alone cannot establish that no new next page appeared.
                data, headers = reader.get(f"{resource}?per_page=100&page={position + 1}", etag=checkpoint["etag"] if checkpoint["more"] else None)
                unchanged = headers.get("_triage_not_modified") is True
                more = bool(re.search(r'rel="next"', headers.get("link", ""))) if not unchanged or "link" in headers else checkpoint["more"]
                if (not unchanged and canonical(data) != canonical(rows)) or more != checkpoint["more"]:
                    matches = False
                    break

            if matches:
                for checkpoint, rows in saved:
                    values.extend(rows)
                    seen.update(page_keys(name, rows))

                page = len(saved) + 1
                component["pagination_complete"] = not saved[-1][0]["more"]
                reader.resumed_pages += len(saved)
            else:
                job.reset()
                saved = []

        while not component["pagination_complete"]:
            if name == "files" and len(values) >= 3000:
                component["truncated"] = True
                raise ReadFailure("GitHub files endpoint caps results at 3000 files")

            data, headers = reader.get(f"{resource}?per_page=100&page={page}")
            try:
                keys = set(page_keys(name, data))
            except ValueError as error:
                raise ReadFailure(str(error)) from error

            if seen & keys:
                raise ReadFailure("repeated collection identity; pagination may have shifted")

            more = bool(re.search(r'rel="next"', headers.get("link", "")))
            if job:
                job.append(data, more, headers.get("etag"), now())

            seen.update(keys)
            values.extend(data)
            if name == "files" and len(values) >= 3000 and (more or expected is None or expected > len(values)):
                component["truncated"] = True
                raise ReadFailure("GitHub files endpoint caps results at 3000 files")

            if not more:
                component["pagination_complete"] = True
                break

            page += 1

        if expected is not None and len(values) != expected:
            if name == "files" and len(values) >= 3000:
                component["truncated"] = True

            raise ReadFailure("received count differs from summary; collection may have changed")

        if name == "files" and any("patch" not in row for row in values):
            raise ReadFailure("file list fetched, but patches are omitted (possibly binary or oversized); code evidence is incomplete")

        component.update(status="complete", error=None)
    except ReadFailure as error:
        component.update(status="partial" if values else "unavailable" if error.unavailable else "failed", error=str(error))

    # Reused bytes retain their earliest observation time. Revalidation is not permission to erase their age.
    component.update(fetched_at=job.pages[0]["fetched_at"] if job and values and job.pages else now(), received_count=len(values))
    attach(component, values, payloads)

    return component


def acquire(cache, kind, number, requested, mode, max_age, budget, reader=None, expected_snapshot=None, reuse_snapshot=None):
    """Serialize selected-item acquisition only, never ledger edits. Immutable component checkpoints survive interruption."""
    cache.initialize()
    with locked(cache.path("acquisition")):
        previous = cache.latest(kind, number)
        if expected_snapshot is not None and (previous is None or previous[0]["snapshot_id"] != expected_snapshot):
            raise ValueError("poll acquisition snapshot changed; explicitly restart the poll")

        # Another acquisition may have satisfied this request while it waited for the lock.
        if mode == "cache-preferred" and previous and all(name in previous[1]["components"] and not component_problems(name, previous[1]["components"][name], previous[1]["revision"], now(), max_age) for name in requested):
            return previous[0], dict(requests=0, cache_hits=len(requested))

        # An explicitly selected prior dataset can supply reusable components after a list-only observation. Never replace a newer partial detail observation with older complete evidence.
        if reuse_snapshot is not None and previous and set(previous[1]["components"]) == {"summary"}:
            summary_ref = previous[1]["components"]["summary"]["object"]
            listed = json.loads(cache.read_object(summary_ref)) if summary_ref else {}
            if listed.get("inventory", {}).get("artifact") == "inventory-observation":
                donor = cache.load(reuse_snapshot)
                same_repository(donor["repository"], cache.require_ready(bound=True)["repository"])
                record = next((row for row in donor["items"] if (row["identity"]["kind"], row["identity"]["number"]) == (kind, number)), None)
                if record is None:
                    raise ValueError("reuse snapshot is missing its dataset member")

                same_item(previous[1]["identity"], record["identity"])
                previous = (donor, record)

        reader = reader or GitHubReader(cache.identity["host"], budget, Cooldown(cache))
        if reader.host != cache.identity["host"]:
            raise ValueError("acquisition reader host mismatch")

        initial_requests, initial_pages = reader.requests, reader.resumed_pages
        started = now()
        repo_resource = f"repos/{cache.identity['full_name']}"
        raw_repo, _ = reader.get(repo_resource)
        observed = repository(raw_repo["full_name"], host=cache.identity["host"], database_id=raw_repo["id"], node_id=raw_repo["node_id"])
        cache.bind_repository(observed)
        resource = f"{repo_resource}/{'pulls' if kind == 'pr' else 'issues'}/{number}"
        raw, _ = reader.get(resource)
        identity, revision = summary_identity(raw, kind, number, observed)
        if previous:
            same_item(previous[1]["identity"], identity)

        old_summary = previous[1]["components"].get("summary") if previous else None
        old_scope = check_scope(json.loads(cache.read_object(old_summary["object"]))) if old_summary and old_summary["object"] else None
        payloads = {}
        # Keep previously observed components when a narrower profile is refreshed; their own timestamps/revisions still control reuse.
        to_fetch = requested
        components = copy.deepcopy(previous[1]["components"]) if previous else {}
        components.update({name: descriptor(resource, revision) for name in requested})
        if kind == "issue":
            for name in PR_ONLY:
                if name in components:
                    components[name] = dict(descriptor(resource, revision), status="not_applicable", fetched_at=None, source=None, error=None)

        requested = list(dict.fromkeys(requested + list(components)))
        record = dict(identity=identity, revision=revision, components=components)
        summary = descriptor(resource, revision, "partial", "final metadata recheck pending")
        attach(summary, dict(raw, state="merged" if kind == "pr" and raw["merged"] else raw["state"]), payloads)
        components["summary"] = summary

        def checkpoint():
            manifest = seal_snapshot(dict(schema_version=1, artifact="evidence-snapshot", repository=observed, started_at=started, completed_at=now(), requested_components=requested, items=[record]))
            refs = {object_name(c["object"]) for c in components.values() if c["object"]}
            cache.publish(manifest, {key: value for key, value in payloads.items() if key in refs})
            return manifest

        checkpoint()
        hits = 0
        jobs = {}
        for name in to_fetch:
            if name == "summary":
                continue

            if kind == "issue" and name in PR_ONLY:
                continue

            count_key = {"comments": "comments", "files": "changed_files", "review_comments": "review_comments"}.get(name)
            expected = raw.get(count_key) if count_key else None
            if expected is not None:
                natural(expected, "source count")

            old = previous[1]["components"].get(name) if previous else None
            scope_matches = name != "checks" or old_scope == check_scope(raw)
            if mode not in ("refresh", "poll-resume") and old and scope_matches and (expected is None or old["received_count"] == expected) and not component_problems(name, old, revision, now(), max_age):
                components[name] = copy.deepcopy(old)
                hits += 1
                checkpoint()
                continue

            endpoint = f"{repo_resource}/issues/{number}/{name}" if name in ("comments", "timeline") else f"{resource}/{'comments' if name == 'review_comments' else name}"
            if name in ("diff", "checks", "closing_issues"):
                # Imported lazily to keep the transport/contracts independent of the PR-specific collectors.
                from _pr_evidence import collect_checks, collect_closing, collect_diff

                if name == "diff":
                    files = components["files"]
                    ref = object_name(files["object"]) if files["object"] else None
                    file_data = json.loads(payloads[ref] if ref in payloads else cache.read_object(files["object"])) if ref else []
                    components[name] = collect_diff(reader, resource, revision, files, file_data, raw, payloads)
                elif name == "checks":
                    components[name] = collect_checks(reader, observed, raw, revision, payloads)
                else:
                    components[name] = collect_closing(reader, observed, identity, revision, payloads)
            else:
                job = PageJob(cache, observed, identity, name, endpoint, revision, expected, json.loads(canonical(check_scope(raw))))
                jobs[name] = job
                components[name] = collect(reader, endpoint, name, revision, expected, payloads, job, mode, max_age)

            checkpoint()

        stable = False
        try:
            final, _ = reader.get(resource)
            final_identity, final_revision = summary_identity(final, kind, number, observed)
            same_item(identity, final_identity)
            record["revision"] = final_revision
            counts_changed = any(raw.get(key) != final.get(key) for key in ("comments", "changed_files", "review_comments", "state", "merged", "closed_at"))
            if revision != final_revision or counts_changed or check_scope(raw) != check_scope(final):
                for name, component in components.items():
                    if name != "summary" and component["status"] == "complete":
                        component.update(status="partial", error="item revision or counts changed during acquisition; refresh required")
            else:
                stable = True

            summary = descriptor(resource, final_revision, "complete", None)
            attach(summary, dict(final, state="merged" if kind == "pr" and final["merged"] else final["state"]), payloads)
            components["summary"] = summary
        except ReadFailure as error:
            components["summary"]["error"] = f"final metadata recheck failed: {error}"

        stats = dict(requests=reader.requests - initial_requests, cache_hits=hits)
        if reader.resumed_pages > initial_pages:
            stats["resumed_pages"] = reader.resumed_pages - initial_pages

        if reader.stopped:
            stats["stopped"] = reader.stopped

        manifest = checkpoint()
        for name, job in jobs.items():
            if stable and components[name]["status"] == "complete":
                job.finish("complete")
            elif not stable and components["summary"]["status"] == "complete":
                job.finish("invalidated")

        return manifest, stats
