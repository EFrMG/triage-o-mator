"""Bounded CI metadata views of verified, selected PR evidence; observations are not merge policy."""

from urllib.parse import urlsplit

from _acquire import ReadFailure, now, summary_identity
from _chunks import page, window
from _evidence import repository, same_item, same_repository, timestamp
from _reader import read_evidence
from _triage import REPO


FAILURES = {"failure", "timed_out", "cancelled", "action_required", "startup_failure", "stale"}
PENDING = {"queued", "in_progress", "waiting", "pending", "requested"}


def bounded(value, maximum=256):
    if not isinstance(value, str):
        return None

    return "".join(char if char.isprintable() else " " for char in value)[:maximum]


def observed_time(value):
    try:
        return timestamp(value)
    except (ValueError, TypeError):
        return None


def object_field(data, name):
    value = data.get(name)
    if value is None:
        return {}

    if not isinstance(value, dict):
        raise ValueError(f"check source {name} must be an object")

    return value


def source_repository(raw, host):
    if not isinstance(raw, dict):
        raise ValueError("check source repository is unavailable")

    result = repository(raw.get("full_name"), host, raw.get("id"), raw.get("node_id"))
    if result["database_id"] is None or result["node_id"] is None:
        raise ValueError("check source repository lacks stable identity")

    return result


def verify(evidence):
    """Content hashes do not prove typed source scope: verify the raw envelopes before interpreting them."""
    raw = evidence["data"].get("summary")
    entries = evidence["data"].get("checks", [])
    if raw is None:
        if entries:
            raise ValueError("checks cannot be interpreted without their selected PR summary")

        return [], []

    for side in ("base", "head"):
        object_field(object_field(raw, side), "repo")

    normalized = dict(raw, state="closed" if raw.get("state") == "merged" else raw.get("state"))
    identity, revision = summary_identity(normalized, "pr", evidence["identity"]["number"], evidence["repository"])
    same_item(evidence["identity"], identity)
    if revision != evidence["revision"]:
        raise ValueError("selected PR summary revision differs from its manifest")

    allowed = [evidence["repository"]]
    raw_head = object_field(raw, "head").get("repo")
    if raw_head:
        head = source_repository(raw_head, evidence["repository"]["host"])
        if head["full_name"] == allowed[0]["full_name"]:
            same_repository(allowed[0], head)
        else:
            allowed.append(head)

    descriptor = evidence["components"].get("checks")
    sha = descriptor["revision"]["head_sha"] if descriptor else None
    suites, seen = {}, set()
    for entry in entries:
        if not isinstance(entry, dict) or entry.get("kind") not in ("check_suite", "check_run", "status") or not isinstance(entry.get("data"), dict):
            raise ValueError("invalid typed checks envelope")

        source = entry.get("repository")
        expected = next((repo for repo in allowed if isinstance(source, dict) and repo["full_name"] == source.get("full_name")), None)
        if expected is None:
            raise ValueError("checks envelope belongs to a foreign repository")

        same_repository(expected, source)
        fetched = observed_time(entry.get("fetched_at"))
        if entry.get("head_sha") != sha or fetched is None or fetched > timestamp(descriptor["fetched_at"]):
            raise ValueError("checks envelope has a mismatched head or invalid observation time")

        data, kind = entry["data"], entry["kind"]
        identifier = data.get("id")
        key = (source["full_name"], kind, identifier)
        if type(identifier) is not int or identifier <= 0 or key in seen:
            raise ValueError("checks envelope has an invalid or repeated source identity")

        seen.add(key)
        prefix = f"repos/{source['full_name']}"
        if kind == "status":
            resource = f"{prefix}/commits/{sha}/statuses"
        elif kind == "check_suite":
            resource = f"{prefix}/commits/{sha}/check-suites"
            suites[(source["full_name"], identifier)] = data
        else:
            suite_id = object_field(data, "check_suite").get("id")
            if type(suite_id) is not int or suite_id <= 0:
                raise ValueError("check run lacks its suite identity")

            resource = f"{prefix}/check-suites/{suite_id}/check-runs?filter=all"

        if entry.get("resource") != resource or (kind != "status" and data.get("head_sha") != sha):
            raise ValueError("checks source resource or raw head differs from its envelope")

        if isinstance(data.get("repository"), dict):
            same_repository(expected, source_repository(data["repository"], expected["host"]))

    for entry in entries:
        if entry["kind"] != "check_run":
            continue

        data, source = entry["data"], entry["repository"]["full_name"]
        suite = suites.get((source, data["check_suite"]["id"]))
        app = object_field(data, "app")
        if suite is None or app.get("id") != object_field(suite, "app").get("id"):
            raise ValueError("check run has an absent suite or mismatched app")

        if data["check_suite"].get("head_sha", sha) != sha:
            raise ValueError("check run suite has a foreign head")

    for index, entry in enumerate(entries):
        entry["source_index"] = index

    return entries, allowed


def result(data, kind):
    if kind == "status":
        return {"success": "passed", "failure": "failed", "error": "failed", "pending": "pending"}.get(data.get("state"), "unknown")

    if data.get("status") in PENDING:
        return "pending"

    if data.get("status") != "completed":
        return "unknown"

    conclusion = data.get("conclusion")
    if conclusion == "success":
        return "passed"

    if conclusion in FAILURES:
        return "failed"

    return "unknown"


def row(entry, diagnostics=()):
    data, kind = entry["data"], entry["kind"]
    issues = list(diagnostics)
    name = data.get("context") if kind == "status" else data.get("name") if kind == "check_run" else "Check suite"
    if not isinstance(name, str) or not name:
        name = "Unnamed observation"
        issues.append("source name missing")

    if len(name) > 256 or any(not char.isprintable() for char in name):
        issues.append("source name shortened or sanitized; inspect raw evidence")

    url = data.get("target_url") if kind == "status" else data.get("details_url") or data.get("html_url")
    if url is not None:
        try:
            parsed = urlsplit(url) if isinstance(url, str) else None
        except ValueError:
            parsed = None

        if parsed is None or parsed.scheme not in ("https", "http") or not parsed.hostname or parsed.username is not None or len(url) > 2048 or any(not char.isprintable() or char.isspace() for char in url):
            url = None
            issues.append("source URL omitted; inspect raw evidence")

    times = {key: data.get(key) for key in ("started_at", "completed_at", "updated_at")}
    if kind == "status":
        times["updated_at"] = data.get("created_at")

    for key, value in times.items():
        if value is not None and (observed_time(value) is None or observed_time(value) > timestamp(entry["fetched_at"])):
            times[key] = None
            issues.append(f"{key} is invalid")
        else:
            times[key] = bounded(value, 64)

    if times["started_at"] and times["completed_at"] and timestamp(times["started_at"]) > timestamp(times["completed_at"]):
        issues.append("run timing order is invalid")

    if kind == "check_run" and data.get("status") == "completed" and times["completed_at"] is None:
        issues.append("completed_at is missing")

    app = object_field(data, "app")
    value = result(data, kind)
    if issues and any("missing" in issue or "ambiguous" in issue or "invalid" in issue for issue in issues):
        value = "unknown"

    return dict(source=kind.replace("_", "-"), repository=entry["repository"]["full_name"], app=bounded(app.get("name") or app.get("slug")),
                name=bounded(name), status=bounded(data.get("state") if kind == "status" else data.get("status"), 64),
                conclusion=bounded(data.get("conclusion"), 64), url=url, **times, head_sha=entry["head_sha"],
                result=value, problems=issues, internal_id=data["id"], source_index=entry["source_index"],
                log_capture_supported=kind == "check_run" and value == "failed" and data.get("status") == "completed" and data.get("conclusion") in FAILURES and app.get("slug") == "github-actions")


def latest(groups, time_key):
    selected = []
    for entries in groups.values():
        if len(entries) == 1:
            selected.append((entries[0], []))
            continue

        times = [observed_time(entry["data"].get(time_key)) for entry in entries]
        if any(value is None for value in times):
            selected.extend((entry, ["latest observation ambiguous: missing ordering timestamp"]) for entry in entries)
            continue

        newest = max(times)
        candidates = [entry for entry, value in zip(entries, times) if value == newest]
        diagnostics = ["latest observation ambiguous: tied ordering timestamps"] if len(candidates) > 1 else []
        selected.extend((entry, diagnostics) for entry in candidates)

    return selected


def summarize(entries, complete=True):
    runs, statuses, suites = {}, {}, []
    for entry in entries:
        data, source = entry["data"], entry["repository"]["full_name"]
        kind = entry["kind"]
        if kind == "status":
            name = data.get("context")
            if not isinstance(name, str) or not name:
                name = ("unknown", data["id"])

            statuses.setdefault((source, name.casefold() if isinstance(name, str) else name), []).append(entry)
            continue

        app = object_field(data, "app")
        if type(app.get("id")) is not int or app["id"] <= 0:
            raise ValueError("check observation lacks its stable app identity")

        if kind == "check_suite":
            suites.append(entry)
        else:
            name = data.get("name")
            if not isinstance(name, str) or not name:
                name = ("unknown", data["id"])

            runs.setdefault((source, app["id"], name), []).append(entry)

    chosen = latest(statuses, "created_at")
    for group in runs.values():
        if len({entry["data"]["check_suite"]["id"] for entry in group}) > 1:
            chosen.extend((entry, ["latest observation ambiguous: same app/name spans suites without rerun linkage"]) for entry in group)
        else:
            chosen.extend(latest({"runs": group}, "started_at"))

    rows = [row(entry, diagnostics) for entry, diagnostics in chosen]
    selected_rows = {value["source_index"]: value for value in rows}
    for entry in suites:
        suite_runs = [selected_rows[run["source_index"]] for run, _ in chosen if run["kind"] == "check_run" and run["repository"] == entry["repository"] and run["data"]["check_suite"]["id"] == entry["data"]["id"]]
        suite_value = row(entry)
        known_children = complete and suite_runs and all(value["status"] == "completed" and value["result"] in ("passed", "failed") for value in suite_runs)
        if known_children and suite_value["status"] == "completed":
            if suite_value["result"] == "passed" and all(value["result"] == "passed" for value in suite_runs):
                continue
            if suite_value["result"] == "failed" and any(value["result"] == "failed" and value["conclusion"] == suite_value["conclusion"] for value in suite_runs):
                continue

        notes = ["suite observation is not an individual test"]
        if not suite_runs:
            notes.append("suite has no selected retained runs")
        elif entry["data"].get("status") == "completed":
            notes.append("suite conclusion is unresolved by the selected individual results or coverage")

        suite_row = row(entry, notes)
        if suite_row["result"] not in ("pending", "unknown"):
            suite_row["result"] = "unknown"

        rows.append(suite_row)

    rows.sort(key=lambda value: (value["result"] != "failed", value["repository"], value["source"], value["app"] or "", value["name"], value["internal_id"]))
    results = {value["result"] for value in rows}
    observed = "failed" if "failed" in results else "pending" if "pending" in results else "unknown" if "unknown" in results else "passed" if rows else "empty"
    return rows, observed


def failure_logs(rows, evidence, host, priority=None):
    from _ci_logs import associated

    attempted = 0
    lookup_rows = sorted(rows, key=lambda value: (value["repository"], value["internal_id"]) != priority) if priority is not None else rows
    for value in lookup_rows:
        if value["result"] != "failed":
            continue

        log = dict(state="unavailable", windows=[], omissions=[], display_problems=[])
        value["failure_log"] = log
        if value["source"] != "check-run":
            log["problem"] = "This observation has no individual Actions job log linkage"
            continue

        if attempted >= 8:
            log.update(state="not-shown", problem="Failure excerpt lookup limit reached for this page")
            continue

        attempted += 1
        try:
            result = associated(evidence["repository"], evidence["identity"]["number"], evidence["snapshot_id"], value["repository"], value["internal_id"], host)
            if result is None:
                source = evidence["data"]["checks"][value["source_index"]]["data"]
                slug = object_field(source, "app").get("slug")
                if isinstance(slug, str) and slug and slug != "github-actions":
                    log.update(state="unavailable", problem="Failure log acquisition supports GitHub Actions jobs; this provider has no retained Actions job linkage")
                else:
                    log.update(state="missing", problem="No retained failure log for this exact check source and snapshot")
                continue

            packet = result["packet"]
            log.update(state="retained" if result["windows"] else "unavailable", log_object=result["log_object"], capture_snapshot_id=result["capture_snapshot_id"],
                       **{key: packet[key] for key in ("workflow_name", "job_name", "fetched_at", "selection_basis", "download_partial", "binding_revalidated", "omissions")},
                       display_problems=list(result["display_problems"]), continuation=result["continuation"])
            remaining = 8192
            for source in result["windows"]:
                if remaining <= 0:
                    log["display_problems"].append("Failure excerpt display reached its 8192-byte limit")
                    log["continuation"] = dict(log_object=result["log_object"], number=evidence["identity"]["number"], snapshot_id=evidence["snapshot_id"], start_line=source["start_line"])
                    break

                encoded = source["text"].encode("utf-8")
                text = encoded[:remaining].decode("utf-8", errors="ignore")
                clipped = len(encoded) > remaining
                if not text:
                    log["display_problems"].append("Failure excerpt display cannot fit the next UTF-8 source character")
                    log["continuation"] = dict(log_object=result["log_object"], number=evidence["identity"]["number"], snapshot_id=evidence["snapshot_id"], start_line=source["start_line"])
                    break

                end_line = source["start_line"] + len(text.splitlines(keepends=True)) - 1
                log["windows"].append(dict(start_line=source["start_line"], end_line=end_line, text=text, display_truncated=source["display_truncated"] or clipped, section_title=source.get("section_title")))
                remaining -= len(text.encode("utf-8"))
                if clipped:
                    log["display_problems"].append("Source line display was shortened; read the exact cached log object for full retained context")
                    log["continuation"] = dict(log_object=result["log_object"], number=evidence["identity"]["number"], snapshot_id=evidence["snapshot_id"], start_line=end_line)
                    break

            if not result["windows"]:
                log["problem"] = "Selected capture retained no log bytes; inspect its omissions"
            elif not log["windows"]:
                log.update(state="not-shown", problem="Retained log source could not fit the bounded display")
        except (OSError, ValueError, KeyError, TypeError) as error:
            log.clear()
            log.update(windows=[], omissions=[], display_problems=[])
            log.update(state="invalid", problem=bounded(str(error), 512))


def acquire_failure_logs(rows, evidence, host, previous, budget):
    from _ci_logs import associated, capture, reuse

    result = dict(requests=0, reserved_requests=0, attempted=0, reused=0, omissions=[])
    lookups = 0
    for value in rows:
        if not value["log_capture_supported"]:
            continue

        if value["head_sha"] != (evidence["revision"] or {}).get("head_sha"):
            result["omissions"].append("Failure log acquisition skipped: retained check belongs to a different PR head")
            continue

        if lookups >= 8:
            result["omissions"].append("Automatic failure log lookup limit reached for this page")
            break

        lookups += 1
        try:
            existing = associated(evidence["repository"], evidence["identity"]["number"], evidence["snapshot_id"], value["repository"], value["internal_id"], host)
            if existing is not None:
                continue

        except (OSError, ValueError, KeyError, TypeError):
            # Invalid current selections remain visible; never replace them by searching older objects.
            continue

        try:
            if reuse(evidence["identity"]["number"], evidence["snapshot_id"], previous, value["repository"], value["internal_id"], host):
                result["reused"] += 1
                continue
        except (OSError, ValueError, KeyError, TypeError):
            # An unusable prior source cannot supply reused evidence; capture the current source instead.
            pass

        remaining = budget - result["requests"] - result["reserved_requests"]
        if result["attempted"] >= 3 or remaining < 20:
            result["omissions"].append("Automatic failure log acquisition reached its page or shared request limit")
            break

        result["attempted"] += 1
        try:
            captured = capture(evidence["identity"]["number"], evidence["snapshot_id"], value["internal_id"], value["repository"], host=host, budget=min(remaining, 100), maximum=262144)
        except (OSError, ValueError, KeyError, TypeError, ReadFailure) as error:
            result["omissions"].append(bounded(str(error), 512))
            continue

        result["requests"] += captured["requests"]
        result["reserved_requests"] += captured["reserved_requests"]
        if captured["status"] != "retained":
            result["omissions"].extend(captured["omissions"][:2])

    result["omissions"] = result["omissions"][:6]
    return result


def read(number, mode="offline", host="github.com", snapshot_id=None, max_age=86400, budget=100, offset=0, limit=20, include_logs=False, failure_log_check_id=None, failure_log_source_repo=None, acquire_logs=False):
    window(offset, limit, 50)
    if offset and snapshot_id is None:
        raise ValueError("continuation requires an explicit --snapshot; select the first page's snapshot ID")
    if not 0 <= budget <= 500:
        raise ValueError("request budget must be between 0 and 500")
    priority = None
    if failure_log_check_id is not None or failure_log_source_repo is not None:
        if type(failure_log_check_id) is not int or failure_log_check_id < 1 or not failure_log_source_repo or not include_logs or snapshot_id is None or mode != "offline":
            raise ValueError("failure log priority requires both check/source pins, included logs and a fixed offline snapshot")

        priority = (failure_log_source_repo, failure_log_check_id)

    if acquire_logs and (mode == "offline" or snapshot_id is not None):
        raise ValueError("automatic failure logs require an explicit online checks read without a fixed snapshot")

    previous = None
    if acquire_logs:
        from _cache import EvidenceCache
        try:
            cached = EvidenceCache(repository(REPO, host=host)).latest("pr", number)
            previous = cached[0]["snapshot_id"] if cached else None
        except (OSError, ValueError, KeyError, TypeError):
            previous = None

    evidence = read_evidence("pr", number, profile="pr-checks", mode=mode, host=host, max_age=max_age, budget=min(budget, 20) if acquire_logs else budget, snapshot_id=snapshot_id)
    entries, allowed = verify(evidence)
    checks_descriptor = evidence["components"].get("checks")
    rows, observed = summarize(entries, complete=checks_descriptor is not None and checks_descriptor["status"] == "complete")
    problems = [f"{name}: {problem}" for name, values in evidence["problems"].items() for problem in values]
    raw_summary = evidence["data"].get("summary")
    if raw_summary and not object_field(raw_summary, "head").get("repo"):
        problems.append("head repository unavailable; fork check coverage is unknown")

    descriptor = evidence["components"].get("checks")
    coverage = descriptor["status"] if descriptor else "missing"
    freshness = "unknown"
    if descriptor:
        fetched = observed_time(descriptor["fetched_at"])
        checks_head = descriptor["revision"]["head_sha"]
        selected_head = (evidence["revision"] or {}).get("head_sha")
        if fetched is not None and checks_head and selected_head:
            age = (timestamp(now()) - fetched).total_seconds()
            freshness = "within-window" if 0 <= age <= max_age and checks_head == selected_head else "stale"
    if descriptor and descriptor.get("error"):
        problems.append("checks acquisition: " + bounded(descriptor["error"], 512))
    if observed in ("passed", "empty") and problems:
        observed = "unknown"

    pagination = page(len(rows), offset, len(rows[offset:offset + limit]))
    snapshot = evidence["snapshot_id"]
    for value in rows:
        value["source_reference"] = dict(snapshot_id=snapshot, component="checks", object=descriptor["object"], index=value["source_index"])

    raw_command = ["bin/cache", "--expected-repo", REPO, "--host", host, "chunk", "--snapshot", snapshot, "--kind", "pr", "--number", str(number), "--component", "checks"] if snapshot and descriptor and descriptor["object"] else None
    counts = {value: sum(row["result"] == value for row in rows) for value in ("passed", "failed", "pending", "unknown")}
    selected_rows = rows[offset:offset + limit]
    if priority is not None and not any(value["source"] == "check-run" and value["result"] == "failed" and (value["repository"], value["internal_id"]) == priority for value in selected_rows):
        raise ValueError("priority failure log check is not a failed check-run on the selected page")
    acquisition = acquire_failure_logs(selected_rows, evidence, host, previous, budget - evidence["stats"]["requests"]) if acquire_logs else None
    if include_logs or acquire_logs:
        failure_logs(selected_rows, evidence, host, priority)

    return dict(repository=evidence["repository"], allowed_repositories=allowed, kind="pr", number=number, mode=mode, snapshot_id=snapshot,
                observed_head_sha=(evidence["revision"] or {}).get("head_sha"), checks_head_sha=descriptor["revision"]["head_sha"] if descriptor else None,
                observed_at=descriptor["fetched_at"] if descriptor else None, coverage=coverage, freshness=freshness, freshness_basis=evidence["freshness_basis"],
                problems=problems[:20], observed_result=observed, result_counts=counts, raw_count=len(entries), historical_or_suite_omissions=len(entries) - len(rows),
                rows=selected_rows, pagination=pagination,
                next_offset=pagination["next_offset"], requests=evidence["stats"]["requests"] + (acquisition["requests"] if acquisition else 0),
                reserved_requests=acquisition["reserved_requests"] if acquisition else 0, failure_log_acquisition=acquisition, raw_command=raw_command)
