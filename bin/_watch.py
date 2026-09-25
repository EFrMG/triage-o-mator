"""Explicit closed-PR watches over pinned cache snapshots. Saved observations are not acknowledgment or approval."""

import json

from _acquire import PROFILES, acquire, now, summary_identity
from _evidence import component_problems, natural, same_item, same_repository, timestamp, validate_item
from _jobs import page_keys, read_record, timeline_key, write_record
from _storage import locked

FIELDS = ("repository", "item", "by", "enrolled_at", "closure", "survivor", "provenance", "observed_closed_at", "observations")
COMPONENTS = PROFILES["closure-watch"]


def path_for(cache, number):
    natural(number, "PR number", 1)

    return cache.path("watches", f"pr-{number}.json")


def selected(cache, snapshot, number):
    manifest = cache.load(snapshot)
    same_repository(cache.require_ready(bound=True)["repository"], manifest["repository"])
    record = next((item for item in manifest["items"] if item["identity"]["kind"] == "pr" and item["identity"]["number"] == number), None)
    if record is None:
        raise ValueError("watch PR is absent from the selected snapshot")

    summary = record["components"].get("summary")
    if not summary or summary["object"] is None:
        raise ValueError("watch needs an observed PR summary")

    raw = json.loads(cache.read_object(summary["object"]))
    # The cache summary normalizes a merged PR's state; restore REST state for the shared identity validator.
    identity, revision = summary_identity(dict(raw, state="closed" if raw["state"] == "merged" else raw["state"]), "pr", number, manifest["repository"])
    same_item(identity, record["identity"])
    if identity != record["identity"]:
        raise ValueError("watch requires fully bound item identity")
    if revision != summary["revision"]:
        raise ValueError("watch summary revision mismatch")

    return manifest, record, raw


def load(cache, number):
    value = read_record(path_for(cache, number), "closed-pr-watch", FIELDS, ("poll",))
    validate(value, cache.require_ready(bound=True)["repository"], number)

    return value


def validate(value, repository, number):
    """Validate stored watch structure without reading source payloads or changing case state."""
    same_repository(repository, value["repository"])
    validate_item(value["item"])
    if value["item"]["kind"] != "pr" or value["item"]["number"] != number or value["item"]["database_id"] is None or value["item"]["node_id"] is None:
        raise ValueError("watch item identity mismatch")

    timestamp(value["enrolled_at"])
    if not isinstance(value["by"], str) or not value["by"].strip():
        raise ValueError("watch attribution is required")

    validate_context(value["closure"], value["survivor"], value["provenance"])
    if value["observed_closed_at"] is not None:
        timestamp(value["observed_closed_at"])
    if not isinstance(value["observations"], list) or not value["observations"] or len(set(value["observations"])) != len(value["observations"]):
        raise ValueError("watch needs unique immutable observations")

    if "poll" in value:
        from _watch_poll import validate_poll

        validate_poll(value)

    return value


def validate_context(closure, survivor, provenance):
    if not isinstance(closure, dict) or set(closure) != {"at", "comment_id", "event_id"}:
        raise ValueError("invalid supplied closure reference")

    if closure["at"] is not None:
        timestamp(closure["at"])

    for key in ("comment_id", "event_id"):
        if closure[key] is not None:
            natural(closure[key], key, 1)

    if survivor is not None:
        natural(survivor, "supplied survivor PR", 1)

    if provenance is not None and (not isinstance(provenance, str) or not provenance.strip()):
        raise ValueError("provenance must be unknown or an attributed description")


def observation(cache, watch, snapshot):
    number = watch["item"]["number"]
    manifest, record, summary = selected(cache, snapshot, number)
    same_item(watch["item"], record["identity"])
    same_repository(watch["repository"], manifest["repository"])
    gaps, activity = [], []
    closure_at = watch["closure"]["at"] or watch["observed_closed_at"]
    boundary = timestamp(closure_at) if closure_at else None
    matched = {"comment_id": False, "event_id": False}

    for name in COMPONENTS:
        component = record["components"].get(name)
        if component is None:
            gaps.append(dict(component=name, problems=["not captured"]))
            continue

        problems = component_problems(name, component, record["revision"], manifest["completed_at"], 2**53)
        if problems:
            gaps.append(dict(component=name, problems=problems, error=component["error"]))

        if name == "summary" or component["object"] is None:
            continue

        rows = json.loads(cache.read_object(component["object"]))
        seen = set()
        for source_offset, row in enumerate(rows):
            try:
                key = timeline_key(row) if name == "timeline" else str(page_keys("comments", [row])[0])
            except (ValueError, TypeError, AttributeError) as error:
                gaps.append(dict(component=name, problems=[str(error)]))
                continue

            if key in seen:
                gaps.append(dict(component=name, problems=["repeated source identity"]))
            seen.add(key)
            field = "comment_id" if name == "comments" else "event_id"
            is_reference = watch["closure"][field] is not None and row.get("id") == watch["closure"][field]
            matched[field] = matched[field] or is_reference
            dates = [row.get("created_at"), row.get("updated_at"), row.get("submitted_at")]
            if name == "timeline" and row.get("event") == "committed":
                dates.append((row.get("committer") or {}).get("date"))

            parsed = []
            for date in dates:
                if date is not None:
                    try:
                        parsed.append(timestamp(date))
                    except ValueError:
                        gaps.append(dict(component=name, problems=["invalid source timestamp"]))

            relation = "unknown" if boundary is None or not parsed else "at-or-after-closure" if max(parsed) >= boundary else "before-closure"
            # Enrollment exposes all historical activity; nothing is silently acknowledged or marked seen.
            activity.append(dict(component=name, source_id=key, source_offset=source_offset, relation=relation, supplied_closure_reference=is_reference, source=row))

    if boundary is None:
        gaps.append(dict(component="closure-context", problems=["closure time unknown; activity cannot be ordered against closure"]))

    for field, found in matched.items():
        if watch["closure"][field] is not None and not found:
            gaps.append(dict(component="closure-context", problems=[f"supplied {field} not observed"]))

    return dict(snapshot_id=snapshot, observed_at=manifest["completed_at"], state=summary["state"], closed_at=summary.get("closed_at"),
                closure_boundary=closure_at, boundary_basis="operator-supplied" if watch["closure"]["at"] else "enrollment-summary",
                complete=not gaps, gaps=gaps, activity=activity, acknowledgment="not-tracked")


def inspect(cache, number):
    watch = load(cache, number)
    observations = [observation(cache, watch, snapshot) for snapshot in watch["observations"]]
    successful = [entry["observed_at"] for entry in observations if entry["complete"]]

    return dict(schema_version=1, artifact="closed-pr-watch-view", watch=watch, observations=observations,
                last_successful_check=max(successful, key=timestamp) if successful else None,
                baseline_complete=observations[0]["complete"], requests=0,
                authority="retained observations grant no approval and acknowledge nothing")


def enroll(cache, number, snapshot, by, closure_at=None, comment=None, event=None, survivor=None, provenance=None):
    if not isinstance(by, str) or not by.strip():
        raise ValueError("watch attribution is required")

    closure = dict(at=closure_at, comment_id=comment, event_id=event)
    validate_context(closure, survivor, provenance)
    manifest, record, summary = selected(cache, snapshot, number)
    if summary["state"] not in ("closed", "merged"):
        raise ValueError("enrollment requires an explicitly observed closed PR")

    if survivor == number:
        raise ValueError("supplied survivor must be another PR")

    path = path_for(cache, number)
    with locked(path):
        if path.exists():
            raise ValueError("PR is already watched; capture another observation")

        watch = dict(schema_version=1, artifact="closed-pr-watch", repository=manifest["repository"], item=record["identity"],
                     by=by, enrolled_at=now(), closure=closure, survivor=survivor, provenance=provenance, observed_closed_at=summary.get("closed_at"), observations=[snapshot])
        observation(cache, watch, snapshot)
        write_record(cache, path, watch)

    return inspect(cache, number)


def capture(cache, number, budget):
    # Watch lock precedes acquisition, which precedes cache metadata. No inventory or ledger locks are acquired.
    path = path_for(cache, number)
    with locked(path):
        watch = load(cache, number)
        if watch.get("poll", {}).get("status") in ("running", "partial", "error"):
            raise ValueError("watch has an unfinished poll; resume or explicitly restart it")

        # Validate retained observations before acquiring anything; corruption never triggers a live fallback.
        for snapshot in watch["observations"]:
            observation(cache, watch, snapshot)

        manifest, stats = acquire(cache, "pr", number, COMPONENTS, "refresh", 0, budget)
        snapshot = manifest["snapshot_id"]
        observation(cache, watch, snapshot)
        if snapshot not in watch["observations"]:
            watch["observations"].append(snapshot)
            watch.pop("checksum")
            write_record(cache, path, watch)

    result = inspect(cache, number)
    result["requests"] = stats["requests"]

    return result
