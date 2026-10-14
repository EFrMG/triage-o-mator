"""Explicit bounded failed-job log acquisition and exact offline source reads."""

import base64
import bisect
import hashlib
import json
import os
import re
import selectors
import subprocess
import time

from _acquire import GitHubReader, ReadFailure, now
from _cache import EvidenceCache
from _checks import FAILURES, bounded, verify
from _evidence import artifact_ref, canonical, digest, fields, repository, same_repository, timestamp, validate_ref
from _jobs import Cooldown
from _pr_evidence import pages
from _reader import read_evidence
from _storage import atomic_writer, locked, sync_directory
from _triage import REPO


MAX_RETAINED = 65536
REDIRECT_RESERVATION = 11
ANSI = re.compile(r"\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07\x1b\r\n]*(?:\x07|\x1b\\))")
GROUP = re.compile(r"##\[group\](.*)")
ERROR = re.compile(rb"(?:##\[error\]|\berror\b|\bfailed\b|\bfailure\b|traceback|exception|fatal)", re.I)


def source_lines(data):
    parts = data.split(b"\n")
    return [part + b"\n" for part in parts[:-1]] + ([parts[-1]] if parts[-1] else [])


def selected(number, snapshot, check_id, source_repo, host):
    evidence = read_evidence("pr", number, profile="pr-checks", snapshot_id=snapshot, host=host)
    entries, allowed = verify(evidence)
    entry = next((entry for entry in entries if entry["kind"] == "check_run" and entry["data"]["id"] == check_id and entry["repository"]["full_name"] == source_repo), None)
    if entry is None or entry["data"].get("status") != "completed" or entry["data"].get("conclusion") not in FAILURES:
        raise ValueError("select a retained completed failed check run in the selected source repository")

    if entry["head_sha"] != evidence["revision"]["head_sha"]:
        raise ValueError("selected check does not belong to the selected PR head")

    return evidence, entry


def association_scope(identity, number, snapshot, source_repo, check_id):
    return dict(repository=identity, number=number, snapshot_id=snapshot, source_repository=source_repo, check_id=check_id)


def association_path(cache, scope):
    return cache.path("ci-logs", digest(canonical(scope)) + ".json")


def publish_association(cache, packet, ref, snapshot=None):
    scope = association_scope(packet["repository"], packet["number"], snapshot or packet["snapshot_id"], packet["source_repository"]["full_name"], packet["check_id"])
    pointer = dict(schema_version=1, artifact="ci-failure-log-pointer", scope=scope, log_object=ref)
    payload = canonical(pointer) + "\n"
    if len(payload.encode("utf-8")) > 4096:
        raise ValueError("CI log association exceeds its bounded size")

    with locked(cache.path("cache.json")):
        cache.require_ready(bound=True)
        path = association_path(cache, scope)
        path.parent.mkdir(exist_ok=True)
        sync_directory(cache.root)
        previous = path.stat().st_blocks * 512 if path.exists() else 0
        used = cache._check_capacity(max(0, cache._estimated_allocation(len(payload.encode("utf-8"))) - previous))
        with atomic_writer(path) as output:
            output.write(payload)

        cache._record_growth(used, path.stat().st_blocks * 512 - previous)


def associated(identity, number, snapshot, source_repo, check_id, host="github.com"):
    """The pointer selects one immutable packet; a failed selection never searches older objects."""
    cache = EvidenceCache(identity)
    scope = association_scope(identity, number, snapshot, source_repo, check_id)
    path = association_path(cache, scope)
    if not path.exists():
        return None

    if not path.is_file() or path.stat().st_size > 4096:
        raise ValueError("CI log association is invalid or exceeds its bounded size")

    with path.open("rb") as source:
        payload = source.read(4097)
    if len(payload) > 4096:
        raise ValueError("CI log association exceeds its bounded size")
    pointer = json.loads(payload)
    fields(pointer, ("schema_version", "artifact", "scope", "log_object"))
    if type(pointer["schema_version"]) is not int or pointer["schema_version"] != 1 or pointer["artifact"] != "ci-failure-log-pointer" or canonical(pointer["scope"]) != canonical(scope):
        raise ValueError("CI log association differs from the selected source/snapshot")

    ref = pointer["log_object"]
    validate_ref(ref)
    if ref["format"] != "json":
        raise ValueError("CI log association must select a typed JSON packet")

    result = read(number, snapshot, ref["sha256"], ref["bytes"], host=host, max_lines=20, pins=dict(check_id=check_id))
    if result["packet"]["source_repository"]["full_name"] != source_repo:
        raise ValueError("CI log association selects a foreign check source")
    with path.open("rb") as source:
        current = source.read(4097)
    if current != payload:
        raise ValueError("CI log association changed during verification; read it again")

    return result


def equivalent_source(old_evidence, old_entry, evidence, entry):
    """Reuse binds the full retained check and suite projections, excluding observation timestamps."""
    same_repository(old_evidence["repository"], evidence["repository"])
    same_repository(old_entry["repository"], entry["repository"])
    if old_evidence["identity"] != evidence["identity"] or old_entry["head_sha"] != entry["head_sha"] or old_entry["data"] != entry["data"]:
        return False

    def suite(parent, child):
        return next(value["data"] for value in parent["data"]["checks"] if value["kind"] == "check_suite" and value["repository"] == child["repository"] and value["data"]["id"] == child["data"]["check_suite"]["id"])

    return suite(old_evidence, old_entry) == suite(evidence, entry)


def reuse(number, snapshot, previous, source_repo, check_id, host):
    if previous is None or previous == snapshot:
        return False

    evidence, entry = selected(number, snapshot, check_id, source_repo, host)
    old_evidence = read_evidence("pr", number, profile="pr-checks", snapshot_id=previous, host=host)
    old_entries, _ = verify(old_evidence)
    old_entry = next((value for value in old_entries if value["kind"] == "check_run" and value["data"]["id"] == check_id and value["repository"]["full_name"] == source_repo), None)
    if old_entry is None:
        return False

    if not equivalent_source(old_evidence, old_entry, evidence, entry):
        return False

    result = associated(evidence["repository"], number, previous, source_repo, check_id, host)
    if result is None or not result["windows"] or not result["packet"]["binding_revalidated"]:
        return False

    publish_association(EvidenceCache(evidence["repository"]), result["packet"], result["log_object"], snapshot=snapshot)
    return True


def api_url(host, resource):
    return f"https://api.github.com/{resource}" if host == "github.com" else f"https://{host}/api/v3/{resource}"


def locate(number, snapshot, check_id, source_repo, host="github.com", budget=20, reader=None):
    if not 1 <= budget <= 100:
        raise ValueError("request budget must be between 1 and 100")

    evidence, entry = selected(number, snapshot, check_id, source_repo, host)
    cache = EvidenceCache(evidence["repository"])
    prefix = f"repos/{source_repo}"
    suite_id = entry["data"]["check_suite"]["id"]
    with locked(cache.path("acquisition")):
        reader = reader or GitHubReader(host, budget, Cooldown(cache))
        candidates = []
        resource = f"{prefix}/actions/runs?check_suite_id={suite_id}&head_sha={entry['head_sha']}"
        for runs in pages(reader, resource, "workflow_runs"):
            for run in runs:
                raw_repo = run.get("repository") or {}
                if not isinstance(raw_repo, dict):
                    raise ValueError("CI workflow repository metadata is malformed")
                same_repository(entry["repository"], repository(raw_repo.get("full_name"), host, raw_repo.get("id"), raw_repo.get("node_id")))
                if run.get("head_sha") != entry["head_sha"] or run.get("check_suite_id") != suite_id or type(run.get("id")) is not int or type(run.get("run_attempt")) is not int or run["id"] < 1 or run["run_attempt"] < 1:
                    raise ValueError("CI discovery returned a mismatched or unidentified workflow run")
                candidates.append(run)

        if len(candidates) != 1:
            raise ValueError("CI run linkage is absent or ambiguous; no job or attempt was selected")

        run_id, attempt = candidates[0]["id"], candidates[0]["run_attempt"]
        run_resource = f"{prefix}/actions/runs/{run_id}/attempts/{attempt}"
        run, _ = reader.get(run_resource)
        matches = []
        for jobs in pages(reader, run_resource + "/jobs", "jobs"):
            matches.extend(job for job in jobs if job.get("check_run_url") == api_url(host, f"{prefix}/check-runs/{check_id}"))

        if len(matches) != 1 or type(matches[0].get("id")) is not int or matches[0]["id"] < 1:
            raise ValueError("selected check has absent or ambiguous job linkage in the current exact attempt; no fallback")

        job = matches[0]
        validate_metadata(run, job, entry["repository"], entry, run_id, attempt, job["id"])

    command = ["bin/pr-check-log", "fetch", "--expected-repo", REPO, "--number", str(number), "--snapshot", snapshot,
               "--check-id", str(check_id), "--source-repo", source_repo, "--run-id", str(run_id), "--attempt", str(attempt), "--job-id", str(job["id"])]
    if host != "github.com":
        command.extend(["--host", host])

    return dict(repository=evidence["repository"], snapshot_id=snapshot, check_id=check_id, source_repository=entry["repository"],
                head_sha=entry["head_sha"], run_id=run_id, attempt=attempt, job_id=job["id"], job_name=bounded(job.get("name")), check_name=bounded(entry["data"].get("name")),
                workflow_name=bounded(run.get("name")), requests=reader.requests, fetch_command=command)


def capture(number, snapshot, check_id, source_repo, host="github.com", budget=20, maximum=1048576, start_line=None):
    if type(budget) is not int or not 1 <= budget <= 100 or type(maximum) is not int or not 1 <= maximum <= 4194304:
        raise ValueError("select bounded request and log byte budgets")
    if any(type(value) is not int or value < 1 for value in (number, check_id)) or start_line is not None and (type(start_line) is not int or start_line < 1):
        raise ValueError("select positive PR, check and source line identities")

    evidence, entry = selected(number, snapshot, check_id, source_repo, host)
    result = dict(repository=evidence["repository"], number=number, snapshot_id=snapshot, source_repository=entry["repository"],
                  head_sha=entry["head_sha"], check_id=check_id, check_name=bounded(entry["data"].get("name")), status="unavailable",
                  capture_stage="locate", request_budget=budget, max_log_bytes=maximum, requests=0, reserved_requests=0, omissions=[])
    if (entry["data"].get("app") or {}).get("slug") != "github-actions":
        result["omissions"].append("Selected check provider is not verified as GitHub Actions; no log acquisition attempted")
        return result

    cache = EvidenceCache(evidence["repository"])
    locator = GitHubReader(host, budget, Cooldown(cache))
    try:
        located = locate(number, snapshot, check_id, source_repo, host=host, budget=budget, reader=locator)
    except (ReadFailure, ValueError, OSError, KeyError, TypeError) as error:
        result.update(requests=locator.requests, omissions=[bounded(str(error), 512)])
        return result

    result.update(requests=locator.requests, capture_stage="fetch",
                  **{key: located[key] for key in ("run_id", "attempt", "job_id", "workflow_name", "job_name")})
    remaining = budget - locator.requests
    if remaining < 1:
        result["omissions"].append("Shared request budget exhausted during job location; no log capture attempted")
        return result

    reader = GitHubReader(host, remaining, Cooldown(cache))
    try:
        captured = fetch(number, snapshot, check_id, source_repo, located["run_id"], located["attempt"], located["job_id"],
                         host=host, budget=remaining, maximum=maximum, start_line=start_line, reader=reader)
    except (ReadFailure, ValueError, OSError, KeyError, TypeError) as error:
        result.update(requests=locator.requests + reader.requests, reserved_requests=getattr(reader, "ci_log_reserved_requests", 0), omissions=[bounded(str(error), 512)])
        return result

    result.update(**{key: captured[key] for key in ("log_object", "status", "workflow_name", "job_name", "fetched_at", "download_partial", "binding_revalidated", "selection_basis", "omissions")},
                  requests=locator.requests + captured["requests"], reserved_requests=captured["reserved_requests"])
    return result


def validate_metadata(run, job, source, entry, run_id, attempt, job_id):
    host, repo = source["host"], source["full_name"]
    prefix = f"repos/{repo}"
    if not isinstance(run, dict) or not isinstance(job, dict):
        raise ValueError("CI run/job metadata is malformed")

    raw_repo = run.get("repository") or {}
    if not isinstance(raw_repo, dict):
        raise ValueError("CI run repository metadata is malformed")
    same_repository(source, repository(raw_repo.get("full_name"), host, raw_repo.get("id"), raw_repo.get("node_id")))
    if any(type(run.get(key)) is not int for key in ("id", "run_attempt", "check_suite_id")) or run.get("id") != run_id or run.get("run_attempt") != attempt or run.get("head_sha") != entry["head_sha"] or run.get("check_suite_id") != entry["data"]["check_suite"]["id"] or run.get("url") != api_url(host, f"{prefix}/actions/runs/{run_id}"):
        raise ValueError("CI run attempt differs from the selected check repository, suite or head")

    if any(type(job.get(key)) is not int for key in ("id", "run_id")) or job.get("id") != job_id or job.get("run_id") != run_id or job.get("head_sha") != entry["head_sha"] or job.get("url") != api_url(host, f"{prefix}/actions/jobs/{job_id}") or job.get("check_run_url") != api_url(host, f"{prefix}/check-runs/{entry['data']['id']}"):
        raise ValueError("CI job differs from the selected check, run or head")
    if job.get("status") != "completed" or job.get("conclusion") not in FAILURES:
        raise ValueError("CI logs require a completed failed job")


def raw_api_supported(env):
    """Probe local CLI help without a GitHub request, bounding help output and runtime."""
    deadline = time.monotonic() + 5
    output = bytearray()
    with subprocess.Popen(["gh", "api", "--help"], stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, env=env) as process:
        try:
            with selectors.DefaultSelector() as selector:
                selector.register(process.stdout, selectors.EVENT_READ)
                while len(output) <= 65536:
                    remaining = deadline - time.monotonic()
                    if remaining <= 0 or not selector.select(remaining):
                        return False

                    block = os.read(process.stdout.fileno(), min(16384, 65537 - len(output)))
                    if not block:
                        try:
                            process.wait(timeout=max(0.01, deadline - time.monotonic()))
                        except subprocess.TimeoutExpired:
                            return False

                        return process.returncode == 0 and b"--allow-escape-sequences" in output

                    output.extend(block)
        finally:
            if process.poll() is None:
                process.kill()
            process.wait()

    return False


def bounded_download(reader, resource, maximum):
    """Reserve the initial request and Go's maximum ten redirects; stdout and runtime are bounded."""
    if reader.stopped or reader.cooldown.blocked(now()):
        raise ReadFailure(reader.stopped or reader.cooldown.blocked(now()))
    if reader.requests + REDIRECT_RESERVATION > reader.budget:
        raise ReadFailure("request budget cannot reserve the log download and up to ten redirects")

    env = dict(os.environ)
    env.pop("GH_DEBUG", None)
    env.pop("GH_FORCE_TTY", None)
    env.update(GH_PAGER="cat", NO_COLOR="1", CLICOLOR_FORCE="0")
    command = ["gh", "api", "--hostname", reader.host, "--method", "GET", "--include", "--header", "Accept: application/vnd.github+json", "--header", "X-GitHub-Api-Version: 2022-11-28", resource]
    if raw_api_supported(env):
        # Modern gh refuses ANSI-bearing text before emitting any body; this pipe retains exact bounded bytes and read() neutralizes controls for presentation.
        command.insert(-1, "--allow-escape-sequences")

    deadline = time.monotonic() + 45
    output = bytearray()
    header_end = None
    truncated = False
    error = None
    with subprocess.Popen(command, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, env=env) as process:
        try:
            with selectors.DefaultSelector() as selector:
                selector.register(process.stdout, selectors.EVENT_READ)
                while True:
                    remaining = deadline - time.monotonic()
                    if remaining <= 0:
                        error = "log download timed out"
                        break

                    if not selector.select(remaining):
                        continue

                    block = os.read(process.stdout.fileno(), 16384)
                    if not block:
                        try:
                            process.wait(timeout=max(0.01, deadline - time.monotonic()))
                        except subprocess.TimeoutExpired:
                            error = "log process did not finish before its deadline"
                        break

                    output.extend(block)
                    if header_end is None:
                        match = re.search(rb"\r?\n\r?\n", output)
                        if match:
                            header_end = match.end()
                            if header_end > 65536:
                                raise ReadFailure("log response headers exceed the bounded limit")
                        elif len(output) > 65536:
                            raise ReadFailure("log response headers exceed the bounded limit")

                    if header_end is not None and len(output) - header_end > maximum:
                        del output[header_end + maximum:]
                        truncated = True
                        break
        finally:
            if process.poll() is None:
                process.kill()
            process.wait()

    if header_end is None:
        raise ReadFailure("log download has no valid final HTTP headers")

    header = bytes(output[:header_end]).decode("ascii", errors="replace").splitlines()
    match = re.fullmatch(r"HTTP/\S+ (\d{3})(?: .*)?", header[0]) if header else None
    if not match:
        raise ReadFailure("log download has an invalid HTTP status")

    status = int(match[1])
    headers = {key.lower(): value.strip() for line in header[1:] for key, sep, value in [line.partition(":")] if sep}
    if status in (403, 429):
        reader.stopped = f"HTTP {status}; CI log acquisition stopped"
        reader.throttle("http-throttle-or-forbidden", headers)
    if headers.get("x-ratelimit-remaining") == "0":
        reader.stopped = "GitHub rate limit exhausted; CI log acquisition stopped"
        reader.throttle("http-exhausted", headers)
    if status != 200:
        raise ReadFailure(f"CI log download returned HTTP {status}", unavailable=status in (404, 410))
    if process.returncode and not truncated and error is None:
        error = "log download ended unsuccessfully"

    return bytes(output[header_end:]), truncated or error is not None, error


def excerpts(data, start_line=None):
    lines = source_lines(data)
    offsets = [0]
    for line in lines:
        offsets.append(offsets[-1] + len(line))

    if start_line is not None:
        if start_line > len(lines):
            raise ValueError("requested start line is outside the downloaded prefix")
        ranges = [(start_line - 1, min(len(lines), start_line + 78))]
        basis = "explicit source line window"
    else:
        anchors = [index for index, line in enumerate(lines) if ERROR.search(line)][:4]
        ranges = [(max(0, index - 10), min(len(lines), index + 11)) for index in anchors]
        basis = "error-pattern leads with surrounding context"
        if not ranges and lines:
            ranges = [(0, min(20, len(lines))), (max(0, len(lines) - 40), len(lines))]
            basis = "head and tail of observed prefix; tail is not necessarily the job's final output"

    merged = []
    for first, end in sorted(ranges):
        if merged and first <= merged[-1][1]:
            merged[-1] = (merged[-1][0], max(end, merged[-1][1]))
        else:
            merged.append((first, end))

    segments, remaining = [], MAX_RETAINED
    for first, end in merged:
        if remaining <= 0:
            break
        begin, finish = offsets[first], min(offsets[end], offsets[first] + remaining)
        raw = data[begin:finish]
        last = bisect.bisect_left(offsets, finish)
        segments.append(dict(start_line=first + 1, end_line=last, start_byte=begin, end_byte=finish,
                             last_line_complete=finish == offsets[last] and raw.endswith(b"\n"), content_base64=base64.b64encode(raw).decode("ascii")))
        remaining -= len(raw)

    return segments, basis, len(lines)


def fetch(number, snapshot, check_id, source_repo, run_id, attempt, job_id, host="github.com", budget=20, maximum=1048576, start_line=None, reader=None):
    if not 1 <= budget <= 100 or not 1 <= maximum <= 4194304 or any(type(value) is not int or value < 1 for value in (number, check_id, run_id, attempt, job_id)) or start_line is not None and start_line < 1:
        raise ValueError("select positive CI identities and bounded request/log budgets")

    evidence, entry = selected(number, snapshot, check_id, source_repo, host)
    cache = EvidenceCache(evidence["repository"])
    source = entry["repository"]
    prefix = f"repos/{source_repo}"
    run_resource = f"{prefix}/actions/runs/{run_id}/attempts/{attempt}"
    job_resource = f"{prefix}/actions/jobs/{job_id}"
    resource = job_resource + "/logs"
    with locked(cache.path("acquisition")):
        reader = reader or GitHubReader(host, budget, Cooldown(cache))
        run, _ = reader.get(run_resource)
        job, _ = reader.get(job_resource)
        validate_metadata(run, job, source, entry, run_id, attempt, job_id)
        membership = None
        for page_number, jobs in enumerate(pages(reader, run_resource + "/jobs", "jobs"), 1):
            membership = next((value for value in jobs if value.get("id") == job_id), None)
            if membership is not None:
                page_resource = f"{run_resource}/jobs?per_page=100&page={page_number}"
                break

        if membership is None:
            raise ValueError("selected job is absent from the exact run attempt")

        validate_metadata(run, membership, source, entry, run_id, attempt, job_id)
        omissions, reserved = [], 0
        data, partial = b"", True
        if reader.requests + REDIRECT_RESERVATION + 2 <= budget:
            reserved = REDIRECT_RESERVATION
            reader.ci_log_reserved_requests = reserved
            try:
                data, partial, error = bounded_download(reader, resource, maximum)
                if error:
                    omissions.append(error)
            except (ReadFailure, OSError) as error:
                omissions.append(str(error))
        else:
            omissions.append("request budget insufficient for bounded log download and metadata revalidation")

        reader.budget -= reserved
        binding_revalidated = False
        try:
            after_run, _ = reader.get(run_resource)
            after_job, _ = reader.get(job_resource)
            validate_metadata(after_run, after_job, source, entry, run_id, attempt, job_id)
            if after_run != run or after_job != job:
                partial = True
                omissions.append("run or job metadata changed during capture; interpret retained log as a partial observation")
            else:
                binding_revalidated = True
        except (ReadFailure, ValueError) as error:
            partial = True
            omissions.append("post-download metadata revalidation unavailable: " + str(error))

        segments, basis, observed_lines = excerpts(data, start_line)
        retained_bytes = sum(segment["end_byte"] - segment["start_byte"] for segment in segments)
        if partial and data:
            omissions.append("download is an observed prefix; complete job log coverage is unknown")
        if retained_bytes < len(data):
            omissions.append("only selected source windows are retained; intervening lines are omitted")
        packet = dict(schema_version=1, artifact="ci-failure-log", repository=evidence["repository"], number=number, snapshot_id=snapshot,
                      source_repository=source, head_sha=entry["head_sha"], check_id=check_id, check_name=bounded(entry["data"].get("name")), run_id=run_id, attempt=attempt, job_id=job_id,
                      check_source=dict(component="checks", object=evidence["components"]["checks"]["object"], index=entry["source_index"]),
                      run=run, job=job, membership=membership, membership_resource=page_resource, log_resource=resource, fetched_at=now(),
                      download_partial=partial, observed_bytes=len(data), observed_lines=observed_lines, observed_sha256=hashlib.sha256(data).hexdigest(),
                      binding_revalidated=binding_revalidated, selection_basis=basis, segments=segments, omissions=omissions,
                      request_budget=budget, max_log_bytes=maximum, requests=reader.requests, reserved_requests=reserved)
        payload = canonical(packet) + "\n"
        ref = artifact_ref(payload)
        if ref["bytes"] > 2000000:
            raise ValueError("CI log metadata packet exceeds the bounded object limit")
        cache.store_object(ref, payload)
        publish_association(cache, packet, ref)

    return dict(log_object=ref, status="retained" if segments else "unavailable", **presentation(packet))


def presentation(packet):
    result = {key: value for key, value in packet.items() if key not in ("run", "job", "membership", "segments")}
    result["segments"] = [{key: value for key, value in segment.items() if key != "content_base64"} for segment in packet["segments"]]
    result["workflow_name"] = bounded(packet["run"].get("name"))
    result["job_name"] = bounded(packet["job"].get("name"))
    return result


def read(number, snapshot, object_sha, object_bytes, host="github.com", start_line=None, max_lines=80, pins=None):
    if not 1 <= max_lines <= 200 or not 1 <= object_bytes <= 2000000 or start_line is not None and start_line < 1:
        raise ValueError("select a bounded log object and source line window")

    cache = EvidenceCache(repository(REPO, host))
    ref = dict(sha256=object_sha, bytes=object_bytes, format="json")
    if cache.object_path(ref).stat().st_size != object_bytes:
        raise ValueError("CI log object size differs from the selected reference")

    packet = json.loads(cache.read_object(ref))
    required = ("schema_version", "artifact", "repository", "number", "snapshot_id", "source_repository", "head_sha", "check_id", "check_name", "run_id", "attempt", "job_id", "check_source", "run", "job", "membership", "membership_resource", "log_resource", "fetched_at", "download_partial", "binding_revalidated", "observed_bytes", "observed_lines", "observed_sha256", "selection_basis", "segments", "omissions", "request_budget", "max_log_bytes", "requests", "reserved_requests")
    fields(packet, required)
    if type(packet["schema_version"]) is not int or packet["schema_version"] != 1 or packet["artifact"] != "ci-failure-log" or packet["number"] != number:
        raise ValueError("CI log packet differs from the selected PR/snapshot")
    if any(type(packet[key]) is not int or packet[key] < 1 for key in ("number", "check_id", "run_id", "attempt", "job_id")) or type(packet["download_partial"]) is not bool or not isinstance(packet["segments"], list):
        raise ValueError("CI log packet has malformed identities or coverage")
    if type(packet["binding_revalidated"]) is not bool or not packet["binding_revalidated"] and not packet["download_partial"]:
        raise ValueError("CI log packet has malformed binding revalidation")
    timestamp(packet["fetched_at"])
    if not isinstance(packet["selection_basis"], str) or len(packet["selection_basis"]) > 500 or not isinstance(packet["omissions"], list) or len(packet["omissions"]) > 10 or any(not isinstance(value, str) or len(value) > 1000 for value in packet["omissions"]):
        raise ValueError("CI log packet presentation metadata is malformed")
    if pins and any(value is not None and packet[key] != value for key, value in pins.items()):
        raise ValueError("CI log packet differs from the selected check/run/job pins")

    evidence, entry = selected(number, packet["snapshot_id"], packet["check_id"], packet["source_repository"]["full_name"], host)
    if packet["snapshot_id"] != snapshot:
        current_evidence, current_entry = selected(number, snapshot, packet["check_id"], packet["source_repository"]["full_name"], host)
        if not packet["binding_revalidated"] or not equivalent_source(evidence, entry, current_evidence, current_entry):
            raise ValueError("CI log reuse differs from its exact verified source evidence")

    same_repository(evidence["repository"], packet["repository"])
    same_repository(entry["repository"], packet["source_repository"])
    expected_source = dict(component="checks", object=evidence["components"]["checks"]["object"], index=entry["source_index"])
    if packet["head_sha"] != entry["head_sha"] or packet["check_source"] != expected_source or packet["check_name"] != bounded(entry["data"].get("name")):
        raise ValueError("CI log packet differs from its selected source evidence")

    validate_metadata(packet["run"], packet["job"], entry["repository"], entry, packet["run_id"], packet["attempt"], packet["job_id"])
    validate_metadata(packet["run"], packet["membership"], entry["repository"], entry, packet["run_id"], packet["attempt"], packet["job_id"])
    prefix = f"repos/{entry['repository']['full_name']}/actions"
    membership_prefix = f"{prefix}/runs/{packet['run_id']}/attempts/{packet['attempt']}/jobs?per_page=100&page="
    if packet["log_resource"] != f"{prefix}/jobs/{packet['job_id']}/logs" or not isinstance(packet["membership_resource"], str) or not re.fullmatch(re.escape(membership_prefix) + r"[1-9][0-9]*", packet["membership_resource"]):
        raise ValueError("CI log source resources differ from the selected identities")
    if any(type(packet[key]) is not int or packet[key] < 0 for key in ("observed_bytes", "observed_lines", "requests", "reserved_requests")) or packet["observed_bytes"] > 4194304 or not isinstance(packet["observed_sha256"], str) or not re.fullmatch(r"[0-9a-f]{64}", packet["observed_sha256"]):
        raise ValueError("CI log observation bounds are malformed")
    if type(packet["request_budget"]) is not int or not 1 <= packet["request_budget"] <= 100 or packet["requests"] + packet["reserved_requests"] > packet["request_budget"] or packet["reserved_requests"] not in (0, REDIRECT_RESERVATION):
        raise ValueError("CI log request bounds are malformed")
    if type(packet["max_log_bytes"]) is not int or not 1 <= packet["max_log_bytes"] <= 4194304 or packet["observed_bytes"] > packet["max_log_bytes"]:
        raise ValueError("CI log transfer bounds are malformed")
    rendered = []
    total = 0
    previous_byte = 0
    previous_line = 0
    remaining_lines = max_lines
    remaining_bytes = 3 * MAX_RETAINED
    next_line = None
    started = start_line is None
    for segment in packet["segments"]:
        fields(segment, ("start_line", "end_line", "start_byte", "end_byte", "last_line_complete", "content_base64"))
        if any(type(segment[key]) is not int for key in ("start_line", "end_line", "start_byte", "end_byte")) or not previous_line < segment["start_line"] <= segment["end_line"] <= packet["observed_lines"] or not previous_byte <= segment["start_byte"] < segment["end_byte"] <= packet["observed_bytes"] or type(segment["last_line_complete"]) is not bool:
            raise ValueError("CI log segment bounds are malformed")
        raw = base64.b64decode(segment["content_base64"], validate=True)
        total += len(raw)
        lines = source_lines(raw)
        if len(raw) != segment["end_byte"] - segment["start_byte"] or len(lines) != segment["end_line"] - segment["start_line"] + 1 or total > MAX_RETAINED:
            raise ValueError("CI log segment source ranges are malformed")
        if segment["last_line_complete"] != raw.endswith(b"\n"):
            raise ValueError("CI log segment line completeness is malformed")
        previous_byte, previous_line = segment["end_byte"], segment["end_line"]

        if not started and not segment["start_line"] <= start_line <= segment["end_line"]:
            continue
        first = start_line if not started else segment["start_line"]
        started = True
        if remaining_lines <= 0 or remaining_bytes <= 0:
            if next_line is None:
                next_line = first
            continue

        selected_lines = lines[first - segment["start_line"]:first - segment["start_line"] + remaining_lines]
        decoded = ANSI.sub("", b"".join(selected_lines).decode("utf-8", errors="replace"))
        text = "".join(char if char.isprintable() or char in "\n\t" else " " for char in decoded)
        title = None
        for source_line in lines[:first - segment["start_line"] + 1]:
            marker = GROUP.search(ANSI.sub("", source_line.decode("utf-8", errors="replace")))
            if marker:
                title = bounded(marker.group(1).strip())
            elif b"##[endgroup]" in source_line:
                title = None
        encoded = text.encode("utf-8")
        display_truncated = len(encoded) > remaining_bytes
        text = encoded[:remaining_bytes].decode("utf-8", errors="ignore")
        displayed_lines = len(text.splitlines(keepends=True))
        rendered.append(dict(start_line=first, end_line=first + displayed_lines - 1, text=text, display_truncated=display_truncated, section_title=title))
        remaining_bytes -= len(text.encode("utf-8"))
        remaining_lines -= displayed_lines
        if first + displayed_lines <= segment["end_line"] and next_line is None:
            next_line = first + displayed_lines
    if start_line is not None and not rendered:
        raise ValueError("requested source line is not retained; explicitly capture that window to acquire more context")

    sections = []
    for window in rendered:
        current = None
        for index, line in enumerate(window["text"].splitlines(keepends=True)):
            marker = GROUP.search(line)
            if current is None or marker:
                current = dict(start_line=window["start_line"] + index, end_line=window["start_line"] + index, text="", display_truncated=window["display_truncated"], section_title=bounded(marker.group(1).strip()) if marker else window["section_title"])
                sections.append(current)

            current["text"] += line
            current["end_line"] = window["start_line"] + index

    rendered = sections
    continuation = dict(log_object=ref, number=number, snapshot_id=snapshot, start_line=next_line) if next_line is not None else None
    return dict(log_object=ref, selected_snapshot_id=snapshot, capture_snapshot_id=packet["snapshot_id"], packet=presentation(packet), windows=rendered, continuation=continuation,
                display_problems=["display was bounded; retained source ranges remain available offline"] if continuation or any(window["display_truncated"] for window in rendered) else [])
