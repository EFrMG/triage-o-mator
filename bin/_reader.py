"""One explicit cache-aware evidence reader; compatibility consumers opt in without changing legacy defaults."""

import json

from _acquire import MODES, PROFILES, acquire, now
from _evidence import DEFAULT_MAX_AGE
from _evidence import component_problems, natural, repository, validate_item


def problems(record, requested, max_age):
    result = {}
    for name in requested:
        component = record["components"].get(name) if record else None
        result[name] = component_problems(name, component, record["revision"], now(), max_age) if component else ["missing"]

    return result


def read_evidence(kind, number, profile="discussion", mode="offline", host="github.com", max_age=DEFAULT_MAX_AGE, budget=100, snapshot_id=None):
    validate_item(dict(kind=kind, number=number, database_id=None, node_id=None))
    natural(max_age, "maximum age")
    natural(budget, "request budget", 1)
    if profile not in PROFILES or mode not in MODES:
        raise ValueError("unsupported evidence profile or read mode")

    if snapshot_id is not None and mode != "offline":
        raise ValueError("a fixed snapshot requires offline mode; refresh separately")

    from _cache import EvidenceCache
    from _triage import REPO

    cache = EvidenceCache(repository(REPO, host=host))
    requested = PROFILES[profile]
    if snapshot_id is not None:
        manifest = cache.load(snapshot_id)
        record = next((row for row in manifest["items"] if (row["identity"]["kind"], row["identity"]["number"]) == (kind, number)), None)
        if record is None:
            raise ValueError(f"{kind}:{number} is not in snapshot {snapshot_id}")

        latest = (manifest, record)
    else:
        latest = cache.latest(kind, number)
    issues = problems(latest[1] if latest else None, requested, max_age)
    stats = dict(requests=0, cache_hits=0)
    if mode == "refresh" or (mode == "cache-preferred" and any(issues.values())):
        manifest, stats = acquire(cache, kind, number, requested, mode, max_age, budget)
        latest = (manifest, manifest["items"][0])
    elif latest:
        stats["cache_hits"] = sum(not value for value in issues.values())

    record = latest[1] if latest else None
    components, data = {}, {}
    for name in requested:
        component = record["components"].get(name) if record else None
        components[name] = component
        if component and component["object"]:
            payload = cache.read_object(component["object"])
            data[name] = payload if name == "diff" else json.loads(payload)

    return dict(
        snapshot_id=latest[0]["snapshot_id"] if latest else None,
        repository=latest[0]["repository"] if latest else cache.identity,
        identity=record["identity"] if record else dict(kind=kind, number=number, database_id=None, node_id=None),
        revision=record["revision"] if record else None,
        components=components, data=data, problems=problems(record, requested, max_age),
        mode=mode, freshness_basis="selected snapshot revision, not a guarantee of current GitHub state" if snapshot_id else "latest recorded revision, not a guarantee of current GitHub state", stats=stats,
    )


def enrich_cached(rec, mode, host="github.com", max_age=DEFAULT_MAX_AGE, budget=100, include_diff=False, snapshot_id=None):
    profile = "pr-code" if include_diff and rec["kind"] == "pr" else "discussion"
    evidence = read_evidence(rec["kind"], rec["number"], profile=profile, mode=mode, host=host, max_age=max_age, budget=budget, snapshot_id=snapshot_id)
    data = evidence.pop("data")
    summary = data.get("summary")
    if summary is not None:
        rec.update(body=summary.get("body") or "", title=summary.get("title"), state=summary["state"])
        if "html_url" in summary:
            rec["url"] = summary["html_url"]

        if rec["kind"] == "pr":
            rec.update(additions=summary.get("additions"), deletions=summary.get("deletions"), changed_files=summary.get("changed_files"), is_draft=summary.get("draft"))
            rec["mergeable"] = {True: "MERGEABLE", False: "CONFLICTING", None: "UNKNOWN"}[summary.get("mergeable")]

    if "comments" in data:
        rec["comment_bodies"] = [row.get("body") or "" for row in data["comments"]]
        rec["comment_authors"] = [(row.get("user") or {}).get("login", "") for row in data["comments"]]
        rec["comment_dates"] = [row.get("created_at") or "" for row in data["comments"]]

    if "diff" in data:
        rec["diff_text"] = data["diff"]

    rec["evidence"] = evidence

    return rec
