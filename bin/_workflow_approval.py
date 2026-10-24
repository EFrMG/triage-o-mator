"""Approving workflow runs held for a PR: local holds and agent concerns, the repository's approval mode, recorded approvals and a bounded pass. Every approval binds an offline assessment and its holds, and rechecks live state before each write."""

import json
import os
import re
import subprocess
import uuid
from pathlib import Path

from _acquire import GitHubReader, ReadFailure, now
from _cache import CACHE_DIR
from _chunks import page, window
from _evidence import canonical, digest, natural, timestamp, value_digest
from _jobs import Cooldown
from _storage import atomic_writer, locked
from _triage import CONFIG_DIR, DATA_DIR, REPO, REPO_RE, load_ledger
from _workflow_runs import assess, head_repository, load, ready_cache

POLICY_PATH = CONFIG_DIR / "workflow-approval-mode.json"
MODES = ("off", "stage", "execute")
STATE_DIR = DATA_DIR / "local" / "workflow-approval"
MAX_NOTES = 200
MAX_HISTORY = 200
MAX_REASON_BYTES = 500
MAX_PASS_ITEMS = 20
MAX_RUNS = 20
SHA = re.compile(r"[0-9a-f]{40}")


class TargetChanged(ValueError):
    pass


def is_agent(value):
    return value == "agent" or value.startswith("agent:")


def valid_text(value, limit):
    return isinstance(value, str) and bool(value.strip()) and len(value.encode("utf-8")) <= limit and not any(ord(char) < 32 or ord(char) == 127 for char in value)


def policy():
    if not POLICY_PATH.exists() and not POLICY_PATH.is_symlink():
        return dict(schema_version=1, repositories={})

    value = json.loads(POLICY_PATH.read_text(encoding="utf-8"))
    if not isinstance(value, dict) or set(value) != {"schema_version", "repositories"} or value["schema_version"] != 1 or not isinstance(value["repositories"], dict):
        raise ValueError("workflow approval mode file is malformed")

    for repo, setting in value["repositories"].items():
        if not REPO_RE.fullmatch(repo) or not isinstance(setting, dict) or set(setting) != {"mode"} or setting["mode"] not in MODES:
            raise ValueError("workflow approval repository setting is malformed")

    return value


def policy_status():
    selected = dict(repository=REPO, mode=policy()["repositories"].get(REPO, {}).get("mode", "off"))

    return dict(selected, revision=value_digest(selected))


def set_mode(mode, expected_revision):
    if mode not in MODES or not isinstance(expected_revision, str) or not re.fullmatch(r"[0-9a-f]{64}", expected_revision):
        raise ValueError("select off, stage or execute and pass the expected revision")

    with locked(POLICY_PATH):
        if policy_status()["revision"] != expected_revision:
            raise ValueError("workflow approval mode changed; reload Settings")

        saved = policy()
        saved["repositories"][REPO] = dict(mode=mode)
        with atomic_writer(POLICY_PATH) as out:
            out.write(json.dumps(saved, indent=2, ensure_ascii=False) + "\n")

        return policy_status()


def state_path(number):
    natural(number, "PR number", 1)

    return STATE_DIR / f"pr-{number}.json"


def load_state(number):
    path = state_path(number)
    if not path.exists():
        return dict(schema_version=1, repository=REPO, number=number, notes=[], outcomes=[], staged=[])

    value = json.loads(path.read_text(encoding="utf-8"))
    if not isinstance(value, dict) or value.get("schema_version") != 1 or value.get("repository") != REPO or value.get("number") != number or any(not isinstance(value.get(name), list) for name in ("notes", "outcomes", "staged")):
        raise ValueError(f"workflow approval state for PR #{number} is malformed or belongs to another repository")

    return value


def save_state(value):
    # Outcomes and staging are history and keep the latest entries; notes are never dropped, so a hold cannot silently disappear.
    value["outcomes"], value["staged"] = value["outcomes"][-MAX_HISTORY:], value["staged"][-MAX_HISTORY:]
    path = state_path(value["number"])
    path.parent.mkdir(parents=True, exist_ok=True)
    with atomic_writer(path) as out:
        out.write(json.dumps(value, indent=2, ensure_ascii=False) + "\n")


def active_notes(saved, run_id, head_sha):
    return [note for note in saved["notes"] if note["run_id"] == run_id and note["head_sha"] == head_sha and note["released"] is None]


def note_view(note):
    return dict(id=note["id"], kind="concern" if is_agent(note["by"]) else "hold", by=note["by"], reason=note["reason"], at=note["at"], released=note["released"])


def local_view(number, head_sha):
    """Holds, concerns, staging and outcomes recorded for this PR head, for display beside an assessment; none of it is evidence."""
    saved = load_state(number)
    current = [entry for entry in saved["notes"] if entry["head_sha"] == head_sha]

    return dict(notes=[dict(note_view(note), run_id=note["run_id"]) for note in current],
                earlier_heads=sorted({note["head_sha"] for note in saved["notes"]} - {head_sha}),
                staged=[entry for entry in saved["staged"] if entry["head_sha"] == head_sha],
                outcomes=[outcome_view(entry) for entry in saved["outcomes"] if entry["head_sha"] == head_sha])


def outcome_view(entry):
    return {key: entry.get(key) for key in ("request_id", "run_id", "head_sha", "actor", "by", "status", "http_status", "detail", "started_at", "finished_at")}


def hold(number, run_id, head_sha, by, reason, host):
    """Record a person's hold, or an agent's concern, on one listed run at one head. Either keeps the pass from approving it; neither is posted."""
    if not valid_text(by, 200):
        raise ValueError("--by must name the person or agent")
    if reason is not None and not valid_text(reason, MAX_REASON_BYTES):
        raise ValueError(f"the reason must be one line of at most {MAX_REASON_BYTES} UTF-8 bytes")
    if is_agent(by) and reason is None:
        raise ValueError("an agent's concern needs a reason")
    if not SHA.fullmatch(head_sha or ""):
        raise ValueError("--head must be a 40-character lowercase SHA")

    observation = load(ready_cache(host), number)
    if observation["snapshot_head_sha"] != head_sha or run_id not in {run["id"] for run in observation["runs"]}:
        raise TargetChanged("that run is not in the saved listing for this head; list the runs again")

    path = state_path(number)
    with locked(path):
        saved = load_state(number)
        same = [note for note in active_notes(saved, run_id, head_sha) if note["by"] == by and note["reason"] == reason]
        if not same:
            if len(saved["notes"]) >= MAX_NOTES:
                raise ValueError(f"PR #{number} already has {MAX_NOTES} recorded holds; release some first")

            saved["notes"].append(dict(id=str(uuid.uuid4()), run_id=run_id, head_sha=head_sha, by=by, reason=reason, at=now(), released=None))
            save_state(saved)

    return dict(repository=REPO, number=number, run_id=run_id, head_sha=head_sha, notes=[note_view(note) for note in active_notes(saved, run_id, head_sha)], requests=0)


def release(number, note_id, by):
    """A person releases a hold or a concern; the note stays as history. Agents never release one."""
    if not valid_text(by, 200):
        raise ValueError("--by must name the person")
    if is_agent(by):
        raise ValueError("an agent cannot release a hold or concern; a person reviews it")

    path = state_path(number)
    with locked(path):
        saved = load_state(number)
        note = next((note for note in saved["notes"] if note["id"] == note_id), None)
        if note is None:
            raise TargetChanged("no hold or concern with that ID for this PR")

        if note["released"] is None:
            note["released"] = dict(by=by, at=now())
            save_state(saved)

    return dict(repository=REPO, number=number, note=note_view(note), requests=0)


def script(name, *args):
    result = subprocess.run([str(Path(__file__).absolute().with_name(name)), *args], capture_output=True, text=True)
    if result.returncode:
        raise ValueError(result.stderr.strip() or f"{name} failed")

    return json.loads(result.stdout)


def review_held(number):
    """The Hold actions for Pending review setting also keeps the pass from approving runs on a PR waiting for a person."""
    if not script("action-policy", "status", "--expected-repo", REPO)["pending_review_hold"]:
        return False

    return bool(next((row for row in load_ledger() if row.get("kind") == "pr" and row.get("number") == number), {}).get("review_request"))


def blocked(run, saved, head_sha, mode, held):
    """Why the pass may not approve this run itself; an empty list means it may."""
    reasons = []
    if mode != "execute":
        reasons.append("workflow approval is set to Stage")
    if run["state"] != "normal":
        reasons.append("suspicious: " + ", ".join(sorted({value["rule"] for value in run["flags"]} | set(run.get("pr_rules", [])))))
    for note in active_notes(saved, run["id"], head_sha):
        reasons.append(f"agent concern from {note['by']}" if is_agent(note["by"]) else f"held by {note['by']}")
    if any(entry["run_id"] == run["id"] and entry["head_sha"] == head_sha and entry["status"] == "unknown" for entry in saved["outcomes"]):
        reasons.append("an earlier approval attempt is uncertain; inspect GitHub")
    if held:
        reasons.append("the PR is in Pending review and actions are held for it")

    return reasons


def approval_plan(number, run_ids, by, automation, request_id, host, max_age):
    if not valid_text(by, 200):
        raise ValueError("--by must name the person or agent")
    if is_agent(by) and not automation:
        raise ValueError("an agent approves runs only through the pass, and only normal ones")
    if not run_ids or len(run_ids) > MAX_RUNS or len(set(run_ids)) != len(run_ids):
        raise ValueError(f"select one to {MAX_RUNS} distinct runs")
    if str(uuid.UUID(request_id)) != request_id:
        raise ValueError("request ID must be a canonical UUID")

    assessment = assess(number, host=host, max_age=max_age)
    head = assessment["head_sha"]
    listed = {run["id"]: run for run in assessment["runs"]}
    if any(run_id not in listed for run_id in run_ids):
        raise TargetChanged("a selected run is not in the saved listing; list the runs again")

    saved = load_state(number)
    pr_rules = sorted({value["rule"] for value in assessment["pr_flags"]})
    runs = []
    for run_id in sorted(run_ids):
        run = listed[run_id]
        runs.append(dict(id=run_id, name=run["name"], path=run["path"], event=run["event"], head_repository=run["head_repository"], state=run["state"],
                         rules=sorted(set(pr_rules) | {value["rule"] for value in run["flags"]}),
                         notes=[note_view(note) for note in active_notes(saved, run_id, head)],
                         earlier=[dict(request_id=entry["request_id"], status=entry["status"]) for entry in saved["outcomes"] if entry["run_id"] == run_id and entry["head_sha"] == head]))

    plan = dict(version=1, operation="approve-workflow-runs", request_id=request_id, repository=REPO, host=host, number=number, head_sha=head,
                snapshot_id=assessment["snapshot_id"], assessment_checksum=assessment["checksum"], actor="automation" if automation else "person", by=by, runs=runs)
    if automation:
        mode, held = policy_status()["mode"], review_held(number)
        refusals = {run["id"]: blocked(dict(listed[run["id"]], pr_rules=pr_rules), saved, head, mode, held) for run in runs}
        if any(refusals.values()):
            raise TargetChanged("the pass may not approve these runs: " + "; ".join(f"run {run_id}: {', '.join(reasons)}" for run_id, reasons in refusals.items() if reasons))

    return plan, digest(canonical(plan))


def post_approval(host, run_id):
    """POST the approval and return (HTTP status or None, detail). Never retried here: a lost reply leaves the outcome unknown."""
    env = dict(os.environ)
    env.pop("GH_DEBUG", None)
    command = ["gh", "api", "--hostname", host, "--method", "POST", "--include", "--header", "Accept: application/vnd.github+json",
               "--header", "X-GitHub-Api-Version: 2022-11-28", f"repos/{REPO}/actions/runs/{run_id}/approve"]
    try:
        result = subprocess.run(command, capture_output=True, timeout=60, env=env)
    except (OSError, subprocess.TimeoutExpired) as error:
        return None, f"the request may have reached GitHub ({type(error).__name__})"

    output = result.stdout.decode("utf-8", errors="replace")
    match = re.match(r"HTTP/\S+ (\d{3})", output)
    if not match:
        return None, "GitHub's reply had no HTTP status"

    body = re.split(r"\r?\n\r?\n", output, maxsplit=1)[-1].strip()
    try:
        message = json.loads(body).get("message") if body else None
    except (ValueError, AttributeError):
        message = None

    return int(match[1]), message if isinstance(message, str) else None


def live_checks(reader, plan):
    """The PR is still open at the assessed head from the same head repository; raises TargetChanged before any write otherwise."""
    try:
        pull, _ = reader.get(f"repos/{REPO}/pulls/{plan['number']}")
    except ReadFailure as error:
        raise TargetChanged(f"the PR could not be read before approving ({error})") from error

    if not isinstance(pull, dict) or pull.get("number") != plan["number"] or pull.get("state") != "open" or pull.get("merged") is True:
        raise TargetChanged("the PR is no longer open; nothing was approved")
    if (pull.get("head") or {}).get("sha") != plan["head_sha"]:
        raise TargetChanged("the PR head moved since the assessment; list and assess the runs again")
    if any(run["head_repository"] != head_repository(pull) for run in plan["runs"]):
        raise TargetChanged("the PR's head repository changed since the assessment")


def run_awaiting(reader, plan, run):
    """None when the live run still awaits approval for the assessed head and workflow, otherwise why not."""
    try:
        live, _ = reader.get(f"repos/{REPO}/actions/runs/{run['id']}")
    except ReadFailure as error:
        return f"the run could not be read ({error})"

    if not isinstance(live, dict) or live.get("id") != run["id"] or live.get("head_sha") != plan["head_sha"] or live.get("path") != run["path"] or live.get("event") != run["event"]:
        return "the run no longer matches the assessed head or workflow"
    if (live.get("repository") or {}).get("full_name") != REPO:
        return "the run belongs to another repository"
    if "action_required" not in (live.get("status"), live.get("conclusion")):
        return f"the run is no longer awaiting approval ({live.get('status')}/{live.get('conclusion')})"

    return None


def approve(number, run_ids, by, automation, request_id, approval, host, max_age):
    """Publish an exact reviewed approval plan: recompute it offline, recheck the PR and each run live, then approve one run at a time and record each outcome."""
    path = state_path(number)
    with locked(path):
        saved = load_state(number)
        earlier = [entry for entry in saved["outcomes"] if entry["request_id"] == request_id]
        if earlier:
            if any(entry["approval"] != approval for entry in earlier):
                raise ValueError("request ID already belongs to another plan")

            # A repeated request reports what was recorded and never posts again.
            return dict(repository=REPO, number=number, request_id=request_id, results=[outcome_view(entry) for entry in earlier], repeated=True, requests=0)

        plan, current = approval_plan(number, run_ids, by, automation, request_id, host, max_age)
        if current != approval:
            raise TargetChanged("the runs, their assessment or their holds changed since the preview; review them again")

        cache = ready_cache(host)
        reader = GitHubReader(host, 1 + len(plan["runs"]), Cooldown(cache))
        posts, results = 0, []
        with locked(cache.path("acquisition")):
            live_checks(reader, plan)
            for run in plan["runs"]:
                problem = run_awaiting(reader, plan, run)
                if problem:
                    results.append(dict(run_id=run["id"], status="changed", detail=problem))
                    continue

                entry = dict(request_id=request_id, approval=approval, run_id=run["id"], head_sha=plan["head_sha"], actor=plan["actor"], by=by,
                             state=run["state"], rules=run["rules"], status="unknown", http_status=None, detail=None, started_at=now(), finished_at=None)
                saved["outcomes"].append(entry)
                # Durable before the POST: a lost reply must never invite an automatic second approval.
                save_state(saved)
                posts += 1
                status, detail = post_approval(host, run["id"])
                entry.update(http_status=status, detail=detail, finished_at=now(),
                             status="approved" if status in (200, 201, 204) else "refused" if status is not None and 400 <= status < 500 else "unknown")
                save_state(saved)
                results.append(outcome_view(entry))
                if entry["status"] == "unknown":
                    break

    return dict(repository=REPO, number=number, request_id=request_id, head_sha=plan["head_sha"], results=results,
                stopped=any(row["status"] == "unknown" for row in results), requests=reader.requests + posts)


def selection(numbers):
    if not numbers or len(numbers) > MAX_PASS_ITEMS or len(set(numbers)) != len(numbers):
        raise ValueError(f"select one to {MAX_PASS_ITEMS} distinct PR numbers")

    for number in numbers:
        natural(number, "PR number", 1)

    return sorted(numbers)


def pass_preview(numbers, host, max_age):
    """Offline: which listed runs the pass would approve and which it stages for a person, from saved listings, holds and the repository mode."""
    mode = policy_status()["mode"]
    if mode == "off":
        raise ValueError("workflow approval is off for this repository; choose Stage or Execute normal runs in Settings → Automations")

    items = []
    for number in selection(numbers):
        assessment = assess(number, host=host, max_age=max_age)
        saved, held = load_state(number), review_held(number)
        pr_rules = sorted({value["rule"] for value in assessment["pr_flags"]})
        runs = []
        for run in assessment["runs"]:
            # A run this tool already approved stays listed until the next listing; it is neither staged nor approved again.
            if any(entry["run_id"] == run["id"] and entry["head_sha"] == assessment["head_sha"] and entry["status"] == "approved" for entry in saved["outcomes"]):
                runs.append(dict(id=run["id"], name=run["name"], path=run["path"], state=run["state"], mode="approved", reasons=[]))
                continue

            reasons = blocked(dict(run, pr_rules=pr_rules), saved, assessment["head_sha"], mode, held)
            runs.append(dict(id=run["id"], name=run["name"], path=run["path"], state=run["state"], mode="stage" if reasons else "execute", reasons=reasons))

        items.append(dict(number=number, head_sha=assessment["head_sha"], assessment_checksum=assessment["checksum"], runs=runs))

    plan = dict(version=1, operation="workflow-approval-pass", repository=REPO, host=host, mode=mode, items=items)
    return dict(plan=plan, preview_sha256=digest(canonical(plan)), requests=0)


def pass_run(numbers, by, preview_sha256, host, max_age):
    """Run an exact previewed pass: record staged runs for a person, approve the rest, and stop at the first uncertain outcome."""
    if not valid_text(by, 200):
        raise ValueError("--by must name the agent or person running the pass")

    current = pass_preview(numbers, host, max_age)
    if current["preview_sha256"] != preview_sha256:
        raise TargetChanged("runs, assessments, holds or the mode changed; preview the pass again")

    results, requests, stopped = [], 0, False
    for item in current["plan"]["items"]:
        staged = [run for run in item["runs"] if run["mode"] == "stage"]
        if staged:
            path = state_path(item["number"])
            with locked(path):
                saved = load_state(item["number"])
                keys = {(run["id"], item["head_sha"]) for run in staged}
                saved["staged"] = [entry for entry in saved["staged"] if (entry["run_id"], entry["head_sha"]) not in keys]
                saved["staged"].extend(dict(run_id=run["id"], head_sha=item["head_sha"], state=run["state"], reasons=run["reasons"], by=by, at=now()) for run in staged)
                save_state(saved)

            results.extend(dict(number=item["number"], run_id=run["id"], status="staged", reasons=run["reasons"]) for run in staged)

        chosen = [run["id"] for run in item["runs"] if run["mode"] == "execute"]
        if not chosen:
            continue

        request_id = str(uuid.uuid4())
        try:
            _, approval = approval_plan(item["number"], chosen, by, True, request_id, host, max_age)
            outcome = approve(item["number"], chosen, by, True, request_id, approval, host, max_age)
        except TargetChanged as error:
            results.extend(dict(number=item["number"], run_id=run_id, status="changed", detail=str(error)) for run_id in chosen)
            continue

        requests += outcome["requests"]
        results.extend(dict(row, number=item["number"]) for row in outcome["results"])
        if outcome["stopped"]:
            stopped = True
            break

    return dict(repository=REPO, mode=current["plan"]["mode"], results=results, stopped=stopped, requests=requests)


def numbers_in(directory):
    if not directory.is_dir():
        return set()

    return {int(path.name[3:-5]) for path in directory.iterdir() if re.fullmatch(r"pr-[1-9][0-9]*\.json", path.name)}


def inbox(host, offset, limit):
    """Offline rows for Notifications → Workflows: PRs with runs listed as waiting, staged runs, holds or recorded outcomes. Reading them approves or acknowledges nothing."""
    window(offset, limit, 50)
    numbers = numbers_in(STATE_DIR)
    # Without saved listings or local records there is nothing to show, and an install without a cache yet must not fail Notifications.
    cache = ready_cache(host) if numbers or (CACHE_DIR / "workflow-runs").is_dir() else None
    if cache is not None:
        numbers |= numbers_in(cache.path("workflow-runs"))

    titles = {row.get("number"): row.get("title") for row in load_ledger() if row.get("kind") == "pr"} if numbers else {}
    rows = []
    for number in sorted(numbers):
        try:
            observation = load(cache, number) if cache is not None else None
        except FileNotFoundError:
            observation = None

        saved = load_state(number)
        head = observation["snapshot_head_sha"] if observation else None
        listed_open = observation is not None and observation["live"]["state"] == "open" and not observation["live"]["merged"]
        waiting = [dict(id=run["id"], name=run["name"], path=run["path"]) for run in observation["runs"]] if listed_open else []
        ids = {run["id"] for run in waiting}
        staged = [entry for entry in saved["staged"] if entry["head_sha"] == head and entry["run_id"] in ids]
        notes = [dict(note_view(note), run_id=note["run_id"]) for note in saved["notes"] if note["head_sha"] == head and note["released"] is None and note["run_id"] in ids]
        outcomes = [outcome_view(entry) for entry in saved["outcomes"][-5:]]
        if not waiting and not outcomes:
            continue

        times = [observation["observed_at"]] if observation else []
        times += [entry["at"] for entry in staged] + [note["at"] for note in notes] + [entry["finished_at"] or entry["started_at"] for entry in outcomes]
        rows.append(dict(number=number, title=titles.get(number) if isinstance(titles.get(number), str) else None, head_sha=head, observed_at=observation["observed_at"] if observation else None, waiting=waiting,
                         suspicious=sum(entry["state"] == "suspicious" for entry in staged), staged=staged, notes=notes, outcomes=outcomes,
                         uncertain=sum(entry["status"] == "unknown" for entry in saved["outcomes"]), updated_at=max(times, key=timestamp)))

    rows.sort(key=lambda row: (timestamp(row["updated_at"]), row["number"]), reverse=True)
    chosen = rows[offset:offset + limit]
    return dict(repository=REPO, mode=policy_status()["mode"], rows=chosen, pagination=page(len(rows), offset, len(chosen)), requests=0,
                meaning="Listed runs, staging, holds and outcomes recorded locally. Opening a row neither approves a run nor acknowledges it.")
