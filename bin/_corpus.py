"""Frozen inventory selections and restartable deep acquisition. Local progress is not live coverage or authority."""

import fcntl
import json

from _acquire import GitHubReader, PROFILES, ReadFailure, acquire, now
from _evidence import DEFAULT_MAX_AGE
from _evidence import DIGEST, canonical, digest, fields, natural, same_item, same_repository, timestamp, version
from _jobs import Cooldown, read_record, write_record
from _reader import problems
from _storage import locked

SCOPES = {"open-prs": {"pr"}, "open-issues": {"issue"}, "open-items": {"pr", "issue"}}
PLAN_FIELDS = ("repository", "inventory_snapshot", "scope", "profile", "max_age", "members")
STATE_FIELDS = ("plan_id", "updated_at", "status", "items", "last_run")


def path(cache, identifier, suffix):
    if not isinstance(identifier, str) or not DIGEST.fullmatch(identifier):
        raise ValueError("corpus ID must be a SHA-256 digest")

    return cache.path("corpora", identifier + suffix)


def selection(cache, snapshot_id, scope):
    if scope not in SCOPES:
        raise ValueError("unsupported corpus scope")

    manifest = cache.load(snapshot_id)
    members, provenance = [], None
    for record in manifest["items"]:
        component = record["components"].get("summary")
        if not component or not component["object"]:
            raise ValueError("corpus creation requires an imported full inventory snapshot")

        data = json.loads(cache.read_object(component["object"]))
        observed = data.get("inventory")
        if not isinstance(observed, dict):
            raise ValueError("corpus creation requires inventory provenance, not detail snapshots")

        fields(observed, ("artifact", "schema_version", "repository", "started_at", "completed_at", "endpoint", "pages", "pagination_complete", "mode", "since", "raw_sha256"))
        version(observed, "inventory-observation")
        same_repository(manifest["repository"], observed["repository"])
        if observed["mode"] != "full" or observed["since"] is not None or observed["pagination_complete"] is not True:
            raise ValueError("corpus selection requires a full open-item inventory, not an incremental fetch")

        if observed["endpoint"] != f"repos/{manifest['repository']['full_name']}/issues?state=open&per_page=100":
            raise ValueError("inventory endpoint/scope mismatch")

        if (observed["started_at"], observed["completed_at"]) != (manifest["started_at"], manifest["completed_at"]):
            raise ValueError("inventory observation interval mismatch")

        natural(observed["pages"], "inventory page count", 1)
        if not isinstance(observed["raw_sha256"], str) or not DIGEST.fullmatch(observed["raw_sha256"]):
            raise ValueError("invalid inventory source checksum")

        if provenance is not None and observed != provenance:
            raise ValueError("inventory snapshot mixes source scopes")

        provenance = observed
        identity = record["identity"]
        if data.get("source_state") != "open" or data.get("state") != "open" or (data.get("kind"), data.get("number")) != (identity["kind"], identity["number"]):
            raise ValueError("inventory member identity/state mismatch")

        if identity["kind"] in SCOPES[scope]:
            members.append(identity)

    return manifest["repository"], sorted(members, key=lambda row: (row["kind"], row["number"]))


def create(cache, snapshot_id, scope="open-prs", profile="pr-comparison", max_age=DEFAULT_MAX_AGE, reuse_corpus=None):
    natural(max_age, "maximum age")
    if profile not in PROFILES:
        raise ValueError("unsupported corpus profile")

    repository, members = selection(cache, snapshot_id, scope)
    plan = dict(artifact="evidence-corpus-plan", schema_version=1, repository=repository, inventory_snapshot=snapshot_id, scope=scope, profile=profile, max_age=max_age, members=members)
    if reuse_corpus is not None:
        prior = load_plan(cache, reuse_corpus)
        state = load_state(cache, reuse_corpus, prior)
        # Freeze the selected references, not a pointer to mutable progress. The next acquisition rechecks live identity and revision before reusing any component.
        plan["reuse_snapshots"] = {key(member): state["items"][key(member)]["snapshot_id"] for member in members
                                   if key(member) in state["items"] and state["items"][key(member)]["snapshot_id"] is not None}

        for member in members:
            if key(member) in plan["reuse_snapshots"]:
                pinned(cache, plan["reuse_snapshots"][key(member)], member)

    identifier = digest(canonical(plan))
    target = path(cache, identifier, ".plan.json")
    with locked(target):
        if target.exists():
            load_plan(cache, identifier)
        else:
            write_record(cache, target, plan)

    return dict(corpus_id=identifier, members=len(members), scope=scope, profile=profile, requests=0)


def load_plan(cache, identifier, *, metadata_only=False):
    plan = read_record(path(cache, identifier, ".plan.json"), "evidence-corpus-plan", PLAN_FIELDS, ("reuse_snapshots",))
    if plan["checksum"] != identifier:
        raise ValueError("corpus plan filename/checksum mismatch")

    same_repository(cache.require_ready(bound=True)["repository"], plan["repository"])
    natural(plan["max_age"], "maximum age")
    if plan["profile"] not in PROFILES:
        raise ValueError("unsupported corpus profile")

    if metadata_only:
        if plan["scope"] not in SCOPES:
            raise ValueError("unsupported corpus scope")

        manifest = cache.load_manifest(plan["inventory_snapshot"])
        repository = manifest["repository"]
        members = sorted((row["identity"] for row in manifest["items"] if row["identity"]["kind"] in SCOPES[plan["scope"]]), key=lambda row: (row["kind"], row["number"]))
    else:
        repository, members = selection(cache, plan["inventory_snapshot"], plan["scope"])
    same_repository(plan["repository"], repository)
    if plan["members"] != members:
        raise ValueError("corpus membership differs from its frozen inventory")

    reuse = plan.get("reuse_snapshots", {})
    if not isinstance(reuse, dict) or set(reuse) - {key(member) for member in members}:
        raise ValueError("invalid dataset reuse membership")

    for member in members:
        if key(member) in reuse:
            pinned(cache, reuse[key(member)], member, metadata_only=metadata_only)

    return plan


def key(identity):
    return f"{identity['kind']}:{identity['number']}"


def load_state(cache, identifier, plan, *, metadata_only=False):
    target = path(cache, identifier, ".state.json")
    if not target.exists():
        return dict(artifact="evidence-corpus-state", schema_version=1, plan_id=identifier, updated_at=now(), status="pending", items={key(member): dict(attempts=0, snapshot_id=None, outcome="pending", error=None) for member in plan["members"]}, last_run=None)

    state = read_record(target, "evidence-corpus-state", STATE_FIELDS)
    state.pop("checksum")
    timestamp(state["updated_at"])
    if state["plan_id"] != identifier or state["status"] not in ("pending", "running", "stopped", "interrupted", "finished"):
        raise ValueError("invalid corpus progress identity/status")

    if not isinstance(state["items"], dict) or set(state["items"]) != {key(member) for member in plan["members"]}:
        raise ValueError("corpus progress membership mismatch")

    for member in plan["members"]:
        entry = state["items"][key(member)]
        fields(entry, ("attempts", "snapshot_id", "outcome", "error"))
        natural(entry["attempts"], "attempt count")
        if entry["outcome"] not in ("pending", "running", "complete", "gaps", "error"):
            raise ValueError("invalid corpus item outcome")

        if entry["error"] is not None and not isinstance(entry["error"], str):
            raise ValueError("invalid corpus error")

        if entry["outcome"] == "pending" and (entry["attempts"] != 0 or entry["snapshot_id"] is not None or entry["error"] is not None):
            raise ValueError("pending corpus member has attempted results")

        if entry["outcome"] != "pending" and entry["attempts"] == 0:
            raise ValueError("attempted corpus member has no attempt count")

        if entry["snapshot_id"] is not None:
            if not isinstance(entry["snapshot_id"], str) or not DIGEST.fullmatch(entry["snapshot_id"]):
                raise ValueError("invalid corpus snapshot reference")

            record = None if metadata_only else pinned(cache, entry["snapshot_id"], member)
            if not metadata_only and entry["outcome"] == "complete":
                diagnostics = problems(record, PROFILES[plan["profile"]], plan["max_age"])
                if any(problem != "observation outside freshness window" for values in diagnostics.values() for problem in values):
                    raise ValueError("complete corpus member references incomplete evidence")
        elif entry["outcome"] in ("complete", "gaps"):
            raise ValueError("corpus evidence outcome requires a snapshot")

    if state["status"] == "finished" and any(entry["outcome"] not in ("complete", "gaps") for entry in state["items"].values()):
        raise ValueError("finished corpus pass has unfinished members")

    if state["last_run"] is not None:
        run = state["last_run"]
        fields(run, ("started_at", "request_budget", "requests", "reason"))
        timestamp(run["started_at"])
        if timestamp(run["started_at"]) > timestamp(state["updated_at"]):
            raise ValueError("corpus run interval is reversed")

        natural(run["request_budget"], "request budget", 1)
        natural(run["requests"], "request count")
        if run["requests"] > run["request_budget"] or (run["reason"] is not None and not isinstance(run["reason"], str)):
            raise ValueError("invalid corpus run budget/reason")

    return state


def observed_state(cache, identifier, plan, state):
    """Report an abandoned running checkpoint without rewriting saved progress or waiting for a runner."""
    if state["status"] != "running":
        return state

    runner = path(cache, identifier, ".runner")
    lock_path = runner.parent / "local" / (runner.name + ".lock")
    try:
        with lock_path.open("rb") as lock:
            try:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
            except BlockingIOError:
                return state

            try:
                latest = load_state(cache, identifier, plan, metadata_only=True)
                if latest["status"] != "running":
                    return latest

                latest["status"] = "interrupted"
                latest["last_run"] = dict(latest["last_run"], reason="runner exited without a final checkpoint; resume explicitly")
                return latest
            finally:
                fcntl.flock(lock, fcntl.LOCK_UN)
    except FileNotFoundError:
        state = dict(state, status="interrupted", last_run=dict(state["last_run"], reason="runner lock is missing; resume explicitly"))
        return state


def pinned(cache, snapshot_id, identity, *, metadata_only=False):
    manifest = cache.load_manifest(snapshot_id) if metadata_only else cache.load(snapshot_id)
    record = next((row for row in manifest["items"] if key(row["identity"]) == key(identity)), None)
    if record is None:
        raise ValueError("corpus result snapshot is missing its member")

    same_item(identity, record["identity"])
    return record


def inspect(cache, identifier):
    plan = load_plan(cache, identifier)
    state = observed_state(cache, identifier, plan, load_state(cache, identifier, plan))
    counts = dict(pending=0, running=0, complete=0, gaps=0, error=0)
    for entry in state["items"].values():
        counts[entry["outcome"]] += 1

    current_problems = {}
    for member in plan["members"]:
        entry = state["items"][key(member)]
        record = pinned(cache, entry["snapshot_id"], member) if entry["snapshot_id"] else None
        current_problems[key(member)] = problems(record, PROFILES[plan["profile"]], plan["max_age"])

    elapsed = (timestamp(state["updated_at"]) - timestamp(state["last_run"]["started_at"])).total_seconds() if state["last_run"] else None
    return dict(corpus_id=identifier, plan=plan, progress=state, counts=counts, current_problems=current_problems, elapsed_seconds_at_checkpoint=elapsed, coverage_basis="frozen inventory membership; item outcomes at acquisition time, not current open-backlog coverage", freshness_basis="pinned snapshot revisions and current local age; not live GitHub state")


def progress(cache, identifier):
    """Bounded checkpoint metadata and a nonblocking local runner-lock probe, not a source-object audit."""
    plan = load_plan(cache, identifier, metadata_only=True)
    state = observed_state(cache, identifier, plan, load_state(cache, identifier, plan, metadata_only=True))
    counts = dict(pending=0, running=0, complete=0, gaps=0, error=0)
    for entry in state["items"].values():
        counts[entry["outcome"]] += 1

    last = state["last_run"]
    reason = last["reason"] if last else None
    return dict(corpus_id=identifier, repository=plan["repository"], inventory_snapshot=plan["inventory_snapshot"],
                scope=plan["scope"], profile=plan["profile"], max_age=plan["max_age"], members=len(plan["members"]),
                status=state["status"], updated_at=state["updated_at"] if last else None, declared_counts=counts,
                last_run=dict(last, reason=reason[:500] if reason else reason) if last else None,
                reason_truncated=bool(reason and len(reason) > 500),
                elapsed_seconds_at_checkpoint=(timestamp(state["updated_at"]) - timestamp(last["started_at"])).total_seconds() if last else None,
                verification="checkpoint metadata and nonblocking local runner-lock probe only; no payload audit, live GitHub freshness or current-open-backlog coverage", inspection_requests=0)


def run(cache, identifier, budget=100, *, compact=False, keep_complete=False, item_limit=None, bulk=False):
    natural(budget, "request budget", 1)
    if item_limit is not None:
        natural(item_limit, "item limit", 1)
    plan = load_plan(cache, identifier)
    if bulk:
        if plan["profile"] != "backlog":
            raise ValueError("batched acquisition currently supports the backlog profile")

        return run_bulk(cache, identifier, plan, budget, compact=compact, keep_complete=keep_complete, item_limit=item_limit)

    # Only one runner owns this corpus; inspection reads atomic state without waiting for network I/O.
    with locked(path(cache, identifier, ".runner")):
        state = load_state(cache, identifier, plan)
        reader = GitHubReader(cache.identity["host"], budget, Cooldown(cache))
        state.update(status="running", last_run=dict(started_at=now(), request_budget=budget, requests=0, reason=None))

        def save():
            state["updated_at"] = now()
            state["last_run"]["requests"] = reader.requests
            write_record(cache, path(cache, identifier, ".state.json"), state)

        members = plan["members"]
        if plan["scope"] == "open-items":
            members = sorted(members, key=lambda member: (member["kind"] != "pr", member["number"]))

        if keep_complete:
            # Give every pending member a first attempt before spending another budget on recorded gaps.
            waiting = [member for member in members if state["items"][key(member)]["outcome"] in ("pending", "running")]
            retry = [member for member in members if state["items"][key(member)]["outcome"] in ("gaps", "error")]
            members = waiting + retry

        save()
        attempted = 0
        try:
            for member in members:
                entry = state["items"][key(member)]
                if entry["snapshot_id"] is not None:
                    record = pinned(cache, entry["snapshot_id"], member)
                    latest = cache.latest(member["kind"], member["number"])
                    if entry["outcome"] == "complete" and latest and latest[0]["snapshot_id"] == entry["snapshot_id"] and not any(problems(record, PROFILES[plan["profile"]], plan["max_age"]).values()):
                        continue

                if item_limit is not None and attempted >= item_limit:
                    state["status"] = "stopped"
                    state["last_run"]["reason"] = "item limit reached"
                    break

                if reader.requests >= budget or reader.stopped:
                    state["status"] = "stopped"
                    state["last_run"]["reason"] = reader.stopped or "request budget exhausted"
                    break

                entry.update(attempts=entry["attempts"] + 1, outcome="running", error=None)
                attempted += 1
                save()
                manifest, _ = acquire(cache, member["kind"], member["number"], PROFILES[plan["profile"]], "cache-preferred", plan["max_age"], budget, reader=reader,
                                      reuse_snapshot=entry["snapshot_id"] or plan.get("reuse_snapshots", {}).get(key(member)))
                record = next(row for row in manifest["items"] if key(row["identity"]) == key(member))
                same_item(member, record["identity"])
                gaps = problems(record, PROFILES[plan["profile"]], plan["max_age"])
                entry.update(snapshot_id=manifest["snapshot_id"], outcome="gaps" if any(gaps.values()) else "complete")
                save()
                if reader.stopped or (reader.requests >= budget and any(gaps.values())):
                    state["status"] = "stopped"
                    state["last_run"]["reason"] = reader.stopped or "request budget exhausted"
                    break
            else:
                state["status"] = "finished"
        except KeyboardInterrupt:
            state["status"] = "interrupted"
            state["last_run"]["reason"] = "interrupted by operator; cache checkpoints retained"
        except (ReadFailure, ValueError) as error:
            entry.update(outcome="error", error=str(error))
            state["status"] = "stopped"
            state["last_run"]["reason"] = str(error)

        save()

        return progress(cache, identifier) if compact else inspect(cache, identifier)


def run_bulk(cache, identifier, plan, budget, *, compact=False, keep_complete=False, item_limit=None):
    from _bulk import BATCH_SIZE, acquire_batch

    with locked(path(cache, identifier, ".runner")):
        state = load_state(cache, identifier, plan)
        reader = GitHubReader(cache.identity["host"], budget, Cooldown(cache))
        state.update(status="running", last_run=dict(started_at=now(), request_budget=budget, requests=0, reason=None))

        def save():
            state["updated_at"] = now()
            state["last_run"]["requests"] = reader.requests
            write_record(cache, path(cache, identifier, ".state.json"), state)

        members = sorted(plan["members"], key=lambda member: (member["kind"] != "pr", member["number"]))
        if keep_complete:
            waiting = [member for member in members if state["items"][key(member)]["outcome"] in ("pending", "running")]
            retry = [member for member in members if state["items"][key(member)]["outcome"] in ("gaps", "error")]
            members = waiting + retry

        selected = []
        for member in members:
            entry = state["items"][key(member)]
            if entry["snapshot_id"] is not None:
                record = pinned(cache, entry["snapshot_id"], member)
                latest = cache.latest(member["kind"], member["number"])
                if entry["outcome"] == "complete" and latest and latest[0]["snapshot_id"] == entry["snapshot_id"] and not any(problems(record, PROFILES[plan["profile"]], plan["max_age"]).values()):
                    continue

            selected.append(member)

        save()
        offset = 0
        failed_502 = False
        while offset < len(selected):
            if item_limit is not None and offset >= item_limit:
                state["status"] = "stopped"
                state["last_run"]["reason"] = "item limit reached"
                break

            kind = selected[offset]["kind"]
            size = min(BATCH_SIZE, len(selected) - offset, item_limit - offset if item_limit is not None else BATCH_SIZE)
            batch = [member for member in selected[offset:offset + size] if member["kind"] == kind]
            needed = 3 if kind == "pr" else 2
            if reader.stopped or reader.requests + needed > budget:
                state["status"] = "stopped"
                state["last_run"]["reason"] = reader.stopped or "request budget exhausted"
                break

            for member in batch:
                entry = state["items"][key(member)]
                entry.update(attempts=entry["attempts"] + 1, outcome="running", error=None)

            save()
            try:
                work = [batch]
                while work:
                    part = work.pop(0)
                    needed = 3 if kind == "pr" else 2
                    if reader.stopped or reader.requests + needed > budget:
                        raise ReadFailure(reader.stopped or "request budget exhausted during batch acquisition")

                    try:
                        results = acquire_batch(cache, reader, kind, part)
                    except ReadFailure as error:
                        if error.http_status != 502 or reader.stopped:
                            raise

                        if len(part) == 1:
                            entry = state["items"][key(part[0])]
                            entry.update(outcome="error", error=str(error))
                            failed_502 = True
                            save()
                            continue

                        midpoint = len(part) // 2
                        smaller = [part[:midpoint], part[midpoint:]]
                        work[:0] = smaller
                        for member in part:
                            state["items"][key(member)]["attempts"] += 1

                        save()
                        continue

                    for member, manifest in results:
                        entry = state["items"][key(member)]
                        record = manifest["items"][0]
                        gaps = problems(record, PROFILES[plan["profile"]], plan["max_age"])
                        entry.update(snapshot_id=manifest["snapshot_id"], outcome="gaps" if any(gaps.values()) else "complete")
                        save()
            except KeyboardInterrupt:
                state["status"] = "interrupted"
                state["last_run"]["reason"] = "interrupted by operator; completed cache snapshots retained"
                break
            except (ReadFailure, ValueError, OSError) as error:
                for member in batch:
                    entry = state["items"][key(member)]
                    if entry["outcome"] == "running":
                        entry.update(outcome="error", error=str(error))

                state["status"] = "stopped"
                state["last_run"]["reason"] = str(error)
                break

            offset += len(batch)
        else:
            state["status"] = "stopped" if failed_502 else "finished"
            if failed_502:
                state["last_run"]["reason"] = "one or more single-item reads returned HTTP 502; retry explicitly"

        save()
        return progress(cache, identifier) if compact else inspect(cache, identifier)
