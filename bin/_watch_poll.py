"""Explicit watch polling. Watch and cursor commit together after immutable evidence publication."""

from _acquire import GitHubReader, ReadFailure, acquire, now
from _evidence import DIGEST, component_problems, digest, fields, natural, timestamp
from _jobs import Cooldown, write_record
from _storage import locked
from _watch import COMPONENTS, load, observation, path_for, selected

POLL_FIELDS = ("schema_version", "id", "base_checkpoint", "started_at", "updated_at", "status",
               "snapshot_id", "attempts", "request_budget", "requests", "error")
RESUME_AGE = 900


def validate_poll(watch):
    value = watch["poll"]
    fields(value, POLL_FIELDS)
    if type(value["schema_version"]) is not int or value["schema_version"] != 1:
        raise ValueError("unsupported watch poll schema")

    for key in ("id", "base_checkpoint"):
        if not isinstance(value[key], str) or not DIGEST.fullmatch(value[key]):
            raise ValueError("invalid watch poll binding")

    if value["id"] != digest(value["base_checkpoint"] + value["started_at"]):
        raise ValueError("poll identity does not match its initial watch/time binding")

    if value["status"] not in ("running", "partial", "error", "complete"):
        raise ValueError("invalid watch poll status")

    if timestamp(value["updated_at"]) < timestamp(value["started_at"]):
        raise ValueError("invalid watch poll times")

    natural(value["attempts"], "poll attempts", 1)
    natural(value["request_budget"], "poll request budget", 1)
    natural(value["requests"], "poll requests")
    if value["requests"] > value["request_budget"]:
        raise ValueError("poll requests exceed budget")

    if value["error"] is not None and (not isinstance(value["error"], str) or not value["error"]):
        raise ValueError("invalid poll error")

    if value["snapshot_id"] is not None and value["snapshot_id"] not in watch["observations"]:
        raise ValueError("poll checkpoint references unpublished watch evidence")

    if value["status"] in ("partial", "complete") and (value["snapshot_id"] is None or value["error"] is not None):
        raise ValueError("finished poll attempt requires evidence and no acquisition exception")

    if value["status"] == "error" and value["error"] is None:
        raise ValueError("failed poll attempt requires an error")


def commit(cache, path, watch):
    value = {key: item for key, item in watch.items() if key != "checksum"}
    validate_poll(value)
    write_record(cache, path, value)


def status(cache, number, watch=None):
    watch = watch or load(cache, number)
    latest = None
    successful = None
    baseline = False
    for index, snapshot in enumerate(watch["observations"]):
        latest = observation(cache, watch, snapshot)
        if index == 0:
            baseline = latest["complete"]

        if latest["complete"]:
            successful = latest["observed_at"]

    poll = watch.get("poll")
    if poll and poll["status"] == "complete":
        manifest, record, _ = selected(cache, poll["snapshot_id"], number)
        if any(name not in record["components"] or component_problems(name, record["components"][name], record["revision"], manifest["completed_at"], RESUME_AGE) for name in COMPONENTS):
            raise ValueError("complete poll has incomplete source coverage")

    gaps = latest["gaps"]
    return dict(schema_version=1, artifact="watch-poll-status", repository=watch["repository"], item=watch["item"],
                checkpoint=watch["checksum"], poll=poll, observation_count=len(watch["observations"]),
                latest_snapshot=latest["snapshot_id"], state=latest["state"], latest_complete=latest["complete"],
                latest_gaps=gaps[:20], omitted_gaps=max(0, len(gaps) - 20),
                baseline_complete=baseline, last_successful_check=successful, requests=0,
                meaning="Poll completion is source coverage at observation time, not acknowledgment, appeal classification or approval.")


def poll(cache, number, budget=100, checkpoint=None, restart=False):
    natural(budget, "request budget", 1)
    if restart and checkpoint is not None:
        raise ValueError("restart and continuation are mutually exclusive")

    path = path_for(cache, number)
    with locked(path):
        watch = load(cache, number)
        prior = watch.get("poll")
        if checkpoint is not None and checkpoint != watch["checksum"]:
            raise ValueError("watch checkpoint changed; inspect status before continuing")

        if prior and prior["status"] != "complete" and checkpoint is None and not restart:
            raise ValueError("unfinished poll requires --checkpoint or explicit --restart")

        if checkpoint is not None and (not prior or prior["status"] == "complete"):
            raise ValueError("no unfinished poll to continue")

        # Validate retained evidence before mutation or network access. Offline corruption never triggers acquisition.
        status(cache, number, watch)
        continuing = checkpoint is not None
        resume_snapshot = prior["snapshot_id"] if continuing else None
        started = now()
        if not continuing:
            prior = dict(schema_version=1, id=digest(watch["checksum"] + started), base_checkpoint=watch["checksum"],
                         started_at=started, snapshot_id=None, attempts=0)

        prior.update(updated_at=started, status="running", attempts=prior["attempts"] + 1,
                     request_budget=budget, requests=0, error=None)
        watch["poll"] = prior
        commit(cache, path, watch)
        reader = GitHubReader(cache.identity["host"], budget, Cooldown(cache))
        try:
            manifest, _ = acquire(cache, "pr", number, COMPONENTS,
                                  "poll-resume" if resume_snapshot else "refresh", RESUME_AGE, budget,
                                  reader=reader, expected_snapshot=resume_snapshot)
            snapshot = manifest["snapshot_id"]
            observation(cache, watch, snapshot)
            record = next(row for row in manifest["items"] if row["identity"] == watch["item"])
            complete = all(name in record["components"] and not component_problems(
                name, record["components"][name], record["revision"], manifest["completed_at"], RESUME_AGE) for name in COMPONENTS)
            if snapshot not in watch["observations"]:
                watch["observations"].append(snapshot)

            prior.update(snapshot_id=snapshot, status="complete" if complete else "partial")
        except (ReadFailure, ValueError, OSError, KeyError, TypeError) as error:
            prior.update(status="error", error=str(error))

        prior.update(updated_at=now(), requests=reader.requests)
        # One atomic watch update commits both the published observation reference and its continuation.
        commit(cache, path, watch)
        result = status(cache, number)
        result["requests"] = reader.requests

        return result
