"""Workflow runs awaiting approval for one PR: a bounded read-only listing and an offline, deterministic assessment. Nothing here approves a run."""

import fnmatch
import json
import os
import re
import subprocess

from _acquire import GitHubReader, ReadFailure, now, summary_identity
from _cache import EvidenceCache
from _checks import bounded
from _evidence import canonical, digest, natural, repository, same_item, same_repository, timestamp
from _jobs import Cooldown, read_record, write_record
from _pr_evidence import pages
from _reader import read_evidence
from _storage import locked
from _triage import CONFIG_DIR, REPO, WORK_ROOT

ARTIFACT = "workflow-run-observation"
FIELDS = ("repository", "item", "snapshot_id", "snapshot_head_sha", "live", "observed_at", "runs")
RULES_VERSION = 1
CONFIG_PATH = CONFIG_DIR / "workflow-approval.json"
CONFIG_FIELDS = {"executed_paths", "patterns", "max_changed_files", "max_changed_lines", "max_line_length"}
MAX_LOCATIONS = 10
MAX_WORKFLOW_BYTES = 1048576
# Display bounds for the diff hunks behind flags; the hunks are derived from the pinned snapshot, so they stay outside the checksum.
MAX_HUNKS = 60
MAX_HUNK_LINES = 400
MAX_HUNK_LINE_CHARS = 2000
MAX_HUNK_BYTES = 1048576

# Paths a typical CI job builds or runs. A repository adds its own in config/workflow-approval.json; these cannot be removed.
EXECUTED_PATHS = (
    "Makefile", "*/Makefile", "*.mk", "Dockerfile", "*/Dockerfile", "docker-compose*.yml", "docker-compose*.yaml", ".pre-commit-config.yaml",
    "package.json", "*/package.json", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", ".npmrc", ".yarnrc*",
    "pyproject.toml", "setup.py", "setup.cfg", "requirements*.txt", "tox.ini", "noxfile.py", "conftest.py", "*/conftest.py",
    "Cargo.toml", "Cargo.lock", "build.rs", "*/build.rs", "go.mod", "go.sum", "Gemfile", "Gemfile.lock", "Rakefile",
    "gradlew", "*.gradle", "*.gradle.kts", "pom.xml", "mise.toml", ".mise.toml", ".tool-versions", "scripts/*", "ci/*", ".ci/*",
)

# Added-line patterns that make a run suspicious. A match is a lead for a person, never a verdict; a clean scan never makes a run normal by itself.
PATTERNS = (
    ("pipe-to-shell", r"\b(?:curl|wget)\b[^\n|]*\|\s*(?:sudo\s+)?(?:ba|z|da)?sh\b"),
    ("encoded-payload", r"\bbase64\s+(?:-d|-D|--decode)\b|\bb64decode\b|\batob\s*\("),
    ("dynamic-eval", r"\beval\b|\bexec\s*\(|\bnew\s+Function\s*\("),
    ("credential-reference", r"\bsecrets\.|\bgithub\.token\b|\bGITHUB_TOKEN\b|\bACTIONS_RUNTIME_TOKEN\b|\bACTIONS_ID_TOKEN_REQUEST_(?:TOKEN|URL)\b|\bACTIONS_CACHE_URL\b"),
    ("reverse-shell", r"/dev/tcp/|\bnc\b[^\n]*\s-e\s|\bncat\b|\bsocat\b"),
    ("crypto-miner", r"\b(?:xmrig|minerd|cpuminer|nicehash)\b|stratum\+tcp"),
)
# Network tools are common in documentation examples, so this one pattern skips documentation paths.
NETWORK = ("network-tool", r"\b(?:curl|wget|Invoke-WebRequest|iwr|nslookup|dig)\b")
DOCUMENTATION = ("*.md", "*.rst", "*.txt", "*.adoc", "docs/*", "doc/*")
HOSTED_RUNNER = re.compile(r"(?:ubuntu|windows|macos)-[\w.-]+")


def path_for(cache, number):
    natural(number, "PR number", 1)

    return cache.path("workflow-runs", f"pr-{number}.json")


def ready_cache(host):
    cache = EvidenceCache(repository(REPO, host=host))
    with locked(cache.path("cache.json")):
        cache.require_ready(bound=True)

    return cache


def selected(number, snapshot, host):
    evidence = read_evidence("pr", number, profile="pr-code", mode="offline", host=host, snapshot_id=snapshot)
    summary = evidence["data"].get("summary")
    if summary is None or not evidence["revision"] or not evidence["revision"].get("head_sha"):
        raise ValueError("the selected snapshot has no PR summary with a head commit")

    return evidence, summary


def head_repository(raw):
    repo = (raw.get("head") or {}).get("repo") if isinstance(raw, dict) else None

    return repo.get("full_name") if isinstance(repo, dict) else None


def run_record(run, sha, bound):
    """Keep the run fields an assessment and a later approval need, after checking the run belongs to this repository and head."""
    raw_repo = run.get("repository") or {}
    if not isinstance(raw_repo, dict):
        raise ValueError("workflow run repository metadata is malformed")

    same_repository(bound, repository(raw_repo.get("full_name"), bound["host"], raw_repo.get("id"), raw_repo.get("node_id")))
    if type(run.get("id")) is not int or run["id"] < 1 or run.get("head_sha") != sha:
        raise ValueError("workflow run listing returned an unidentified run or another head commit")

    head = run.get("head_repository") or {}
    attempt = run.get("run_attempt")
    return dict(id=run["id"], name=bounded(run.get("name")), path=bounded(run.get("path"), 512), event=bounded(run.get("event"), 64),
                status=bounded(run.get("status"), 64), conclusion=bounded(run.get("conclusion"), 64), head_sha=sha,
                head_repository=bounded(head.get("full_name")) if isinstance(head, dict) else None,
                run_attempt=attempt if type(attempt) is int and attempt > 0 else None,
                created_at=bounded(run.get("created_at"), 64), html_url=bounded(run.get("html_url"), 2048))


def acquired(number, host, mode, budget):
    """Reuse or acquire a pr-code snapshot through the shared evidence reader; the caller reads it back pinned, like a supplied snapshot."""
    evidence = read_evidence("pr", number, profile="pr-code", mode=mode, host=host, budget=budget)

    return evidence["snapshot_id"], evidence["stats"]["requests"]


def observe(cache, number, evidence, budget):
    """Read the PR's live head and its action_required runs for the snapshot head, under the acquisition lock."""
    sha = evidence["revision"]["head_sha"]
    with locked(cache.path("acquisition")):
        reader = GitHubReader(cache.identity["host"], budget, Cooldown(cache))
        pull, _ = reader.get(f"repos/{REPO}/pulls/{number}")
        identity, revision = summary_identity(pull, "pr", number, evidence["repository"])
        same_item(evidence["identity"], identity)
        live = dict(state=bounded(pull.get("state"), 32), merged=pull.get("merged") is True, head_sha=revision.get("head_sha"), head_repository=head_repository(pull))
        runs = []
        for page in pages(reader, f"repos/{REPO}/actions/runs?status=action_required&head_sha={sha}", "workflow_runs"):
            runs.extend(run_record(run, sha, evidence["repository"]) for run in page)

    value = dict(schema_version=1, artifact=ARTIFACT, repository=evidence["repository"], item=evidence["identity"], snapshot_id=evidence["snapshot_id"],
                 snapshot_head_sha=sha, live=live, observed_at=now(), runs=sorted(runs, key=lambda run: run["id"]))
    return value, reader.requests


def list_runs(number, snapshot=None, host="github.com", budget=6):
    """List the runs for a supplied snapshot, or for an acquired one when snapshot is None, then replace the saved observation."""
    if not 2 <= budget <= 100:
        raise ValueError("request budget must be between 2 and 100")

    cache = ready_cache(host)
    acquire, spent = snapshot is None, 0
    if acquire:
        snapshot, spent = acquired(number, host, "cache-preferred", budget - 2)

    evidence, _ = selected(number, snapshot, host)
    value, used = observe(cache, number, evidence, budget - spent)
    spent += used
    # A reused snapshot may predate the latest push; refresh it once so the runs are listed for the live head.
    if acquire and value["live"]["head_sha"] != value["snapshot_head_sha"] and value["live"]["state"] == "open" and budget - spent > 2:
        snapshot, used = acquired(number, host, "refresh", budget - spent - 2)
        spent += used
        evidence, _ = selected(number, snapshot, host)
        value, used = observe(cache, number, evidence, budget - spent)
        spent += used

    path = path_for(cache, number)
    with locked(path):
        write_record(cache, path, value)

    saved = load(cache, number)
    return dict(repository=REPO, number=number, snapshot_id=saved["snapshot_id"], observed_at=saved["observed_at"], live=saved["live"],
                runs=saved["runs"], checksum=saved["checksum"], requests=spent)


def load(cache, number):
    value = read_record(path_for(cache, number), ARTIFACT, FIELDS)
    same_repository(cache.identity, value["repository"])
    if value["item"].get("kind") != "pr" or value["item"].get("number") != number or not isinstance(value["runs"], list):
        raise ValueError("workflow run observation belongs to another item")

    timestamp(value["observed_at"])
    return value


def config():
    """Repository additions to the built-in rules. A missing file means the defaults; an invalid one is an error, never silently ignored."""
    settings = dict(executed_paths=[], patterns=[], max_changed_files=100, max_changed_lines=3000, max_line_length=1000)
    if not CONFIG_PATH.exists():
        return settings, None

    text = CONFIG_PATH.read_text(encoding="utf-8")
    value = json.loads(text)
    if not isinstance(value, dict) or value.keys() - CONFIG_FIELDS:
        raise ValueError(f"{CONFIG_PATH.name} accepts only {sorted(CONFIG_FIELDS)}")

    for name in ("executed_paths", "patterns"):
        entries = value.get(name, [])
        if not isinstance(entries, list) or any(not isinstance(entry, str) or not entry.strip() for entry in entries):
            raise ValueError(f"{CONFIG_PATH.name} {name} must be a list of non-empty strings")

    for pattern in value.get("patterns", []):
        try:
            re.compile(pattern)
        except re.error as error:
            raise ValueError(f"{CONFIG_PATH.name} pattern {pattern!r} is invalid: {error}") from error

    for name in ("max_changed_files", "max_changed_lines", "max_line_length"):
        if name in value:
            natural(value[name], name, 1)

    settings.update(value)
    return settings, digest(canonical(value))


def parse_diff(text):
    """Split a unified diff into files with their header lines, hunks and added lines (path, new-side line number). Unparsed paths stay None."""
    files, current, line_number = [], None, None
    for line in text.split("\n"):
        if line.startswith("diff --git "):
            # Binary and mode-only entries have no ---/+++ lines; take the path from the header when both sides name the same file.
            rest = line[len("diff --git "):]
            same = rest[2:(len(rest) - 1) // 2] if rest.startswith("a/") else None
            fallback = same if same and rest == f"a/{same} b/{same}" else None
            current = dict(path=fallback, old_path=fallback, headers=[], added=[], hunks=[])
            files.append(current)
            line_number = None
            continue

        if current is None:
            continue

        if line_number is None and not line.startswith("@@"):
            if line.startswith("--- "):
                current["old_path"] = line[6:] if line.startswith("--- a/") else None
            elif line.startswith("+++ "):
                current["path"] = line[6:] if line.startswith("+++ b/") else None
            else:
                current["headers"].append(line)
            continue

        if line.startswith("@@"):
            match = re.match(r"@@ -\d+(?:,\d+)? \+(\d+)(?:,\d+)? @@", line)
            line_number = int(match[1]) if match else 0
            current["hunks"].append(dict(header=line, start=line_number, end=line_number - 1, lines=[]))
            continue

        if line.startswith(("+", "-", " ", "\\")):
            hunk = current["hunks"][-1]
            hunk["lines"].append(line)
            if line.startswith(("+", " ")):
                hunk["end"] += 1

        if line.startswith("+"):
            current["added"].append((line_number, line[1:]))
            line_number += 1
        elif line.startswith(" "):
            line_number += 1

    return files


def flag(category, rule, detail, locations=()):
    locations = list(locations)

    return dict(category=category, rule=rule, detail=detail, locations=locations[:MAX_LOCATIONS], count=len(locations))


def matches(path, patterns):
    return any(fnmatch.fnmatchcase(path, pattern) for pattern in patterns)


def git(*args):
    target = WORK_ROOT.parent
    # Read-only object access in the install's own checkout; fsmonitor and textconv never run.
    return subprocess.run(["git", "-c", "core.fsmonitor=false", "-C", str(target), *args], capture_output=True, timeout=20)


def base_workflows(sha):
    """Workflow files at the PR's base commit from the local object store, or None when that commit is unavailable. Never fetches."""
    try:
        probe = git("rev-parse", "--show-toplevel")
        if probe.returncode or not os.path.samefile(probe.stdout.decode().strip(), WORK_ROOT.parent):
            return None

        if git("cat-file", "-e", f"{sha}^{{commit}}").returncode:
            return None

        listing = git("ls-tree", "-r", "-z", "--name-only", sha, "--", ".github/workflows")
        if listing.returncode:
            return None

        workflows = {}
        for name in listing.stdout.decode().split("\0"):
            if not name.endswith((".yml", ".yaml")):
                continue

            blob = git("cat-file", "blob", f"{sha}:{name}")
            if blob.returncode or len(blob.stdout) > MAX_WORKFLOW_BYTES:
                workflows[name] = None
            else:
                workflows[name] = blob.stdout.decode("utf-8", errors="replace")

        return workflows
    except (OSError, UnicodeError, subprocess.TimeoutExpired):
        return None


def matrix_values(text, name):
    """Every value the workflow assigns to a matrix key, inline or as a list on following lines; None when one cannot be read."""
    values, lines = [], text.split("\n")
    for index, line in enumerate(lines):
        match = re.match(rf"^(\s*)(?:-\s*)?{re.escape(name)}:\s*(.*)$", line)
        if not match:
            continue

        value = match[2].split(" #")[0].strip()
        if value:
            if "${{" in value:
                return None
            values.extend(re.findall(r"[\w.-]+", value))
            continue

        indent = len(match[1])
        for following in lines[index + 1:]:
            item = re.match(r"^(\s*)-\s*(.*)$", following)
            if not item or len(item[1]) <= indent:
                break
            values.extend(re.findall(r"[\w.-]+", item[2].split(" #")[0]))

    return values


def runner_flags(text):
    """Conservative runs-on reading: anything not a plain GitHub-hosted label, including unresolved expressions and multi-line forms, is flagged."""
    labels = []
    for match in re.finditer(r"^\s*runs-on:\s*(.*)$", text, re.MULTILINE):
        value = match[1].split(" #")[0].strip()
        expression = re.fullmatch(r"\$\{\{\s*matrix\.([\w-]+)\s*\}\}", value)
        tokens = matrix_values(text, expression[1]) if expression else None if "${{" in value else re.findall(r"[\w.-]+", value)
        if not value or tokens is None or not tokens or "self-hosted" in value or any(not HOSTED_RUNNER.fullmatch(token) for token in tokens):
            labels.append(value or "(multi-line value)")

    if re.search(r"\bself-hosted\b", text) and not labels:
        labels.append("self-hosted")

    return labels


def executed_by(text):
    """Local paths a workflow runs: scripts named in its steps, local actions, and the Makefile when it calls make."""
    paths = set(re.findall(r"(?<![\w./-])(?:\./)?((?:[\w.-]+/)*[\w.-]+\.(?:sh|bash|py|js|mjs|cjs|ts|rb|pl|ps1))\b", text))
    paths |= {path.rstrip("/") + "/*" for path in re.findall(r"uses:\s*['\"]?\./([\w./-]+)", text)}
    if re.search(r"\bmake\b", text):
        paths.add("Makefile")

    return sorted(paths)


def pr_flags(evidence, summary, observation, settings, max_age):
    data, found = evidence["data"], []
    for name in ("summary", "files", "diff"):
        if evidence["problems"].get(name):
            found.append(flag("state", "evidence-incomplete", f"the snapshot's {name} evidence is incomplete ({'; '.join(evidence['problems'][name])[:300]})"))

    files = data.get("files") if isinstance(data.get("files"), list) else []
    diff = parse_diff(data["diff"]) if isinstance(data.get("diff"), str) else []
    if isinstance(summary.get("changed_files"), int) and summary["changed_files"] != len(files):
        found.append(flag("state", "evidence-incomplete", f"file list has {len(files)} of {summary['changed_files']} changed files"))

    listed = {row.get("filename") for row in files if isinstance(row, dict)}
    parsed = {entry["path"] or entry["old_path"] for entry in diff}
    if None in parsed or parsed != listed:
        found.append(flag("hidden-content", "diff-unparsed", "the diff's file paths could not be matched to the file list; read the raw diff"))

    changed = sorted({path for path in listed | {row.get("previous_filename") for row in files if isinstance(row, dict)} if isinstance(path, str)})
    definition = [dict(path=path) for path in changed if path.startswith(".github/") or path == ".gitmodules" or path.rsplit("/", 1)[-1] in ("action.yml", "action.yaml")]
    if definition:
        found.append(flag("ci-definition", "ci-definition-changed", "the PR changes workflow, action or submodule definitions", definition))

    executed = [dict(path=path) for path in changed if matches(path, EXECUTED_PATHS + tuple(settings["executed_paths"]))]
    if executed:
        found.append(flag("ci-executes", "ci-input-changed", "the PR changes files CI typically builds or runs", executed))

    hidden = {}
    for entry in diff:
        path = entry["path"] or entry["old_path"] or "(unparsed path)"
        for header in entry["headers"]:
            if header.startswith("Binary files ") or header == "GIT binary patch":
                hidden.setdefault("binary-file", []).append(dict(path=path))
            elif header.endswith(" 120000"):
                hidden.setdefault("symlink", []).append(dict(path=path))
            elif header.endswith(" 160000"):
                hidden.setdefault("submodule", []).append(dict(path=path))
            elif header.startswith(("new mode ", "new file mode ")) and header.endswith(" 100755"):
                hidden.setdefault("executable-mode", []).append(dict(path=path))

        long_lines = [dict(path=path, line=number) for number, text in entry["added"] if len(text) > settings["max_line_length"]]
        if long_lines:
            hidden.setdefault("long-line", []).extend(long_lines)

    for row in files:
        pure_rename = isinstance(row, dict) and row.get("status") == "renamed" and row.get("additions") == 0 and row.get("deletions") == 0
        if isinstance(row, dict) and row.get("status") != "removed" and not pure_rename and not isinstance(row.get("patch"), str):
            hidden.setdefault("patch-omitted", []).append(dict(path=row.get("filename")))

    details = {"binary-file": "binary files cannot be reviewed as text", "symlink": "symbolic links can point CI at other files",
               "submodule": "submodule pointers bring in code not shown in the diff", "executable-mode": "files made executable",
               "long-line": f"added lines longer than {settings['max_line_length']} characters, often minified or generated",
               "patch-omitted": "GitHub omitted these files' patches; their content is not reviewable here"}
    found.extend(flag("hidden-content", rule, details[rule], locations) for rule, locations in sorted(hidden.items()))

    rules = list(PATTERNS) + [(f"repository-pattern-{index + 1}", pattern) for index, pattern in enumerate(settings["patterns"])]
    for rule, pattern in rules + [NETWORK]:
        expression = re.compile(pattern)
        hits = [dict(path=entry["path"], line=number) for entry in diff for number, text in entry["added"]
                if expression.search(text) and not (rule == NETWORK[0] and matches(entry["path"] or "", DOCUMENTATION))]
        if hits:
            found.append(flag("risky-pattern", rule, f"added lines match {rule}", hits))

    lines = sum(row.get("additions", 0) + row.get("deletions", 0) for row in files if isinstance(row, dict) and isinstance(row.get("additions"), int) and isinstance(row.get("deletions"), int))
    if len(changed) > settings["max_changed_files"] or lines > settings["max_changed_lines"]:
        found.append(flag("size", "large-change", f"{len(changed)} files and {lines} changed lines exceed the review thresholds ({settings['max_changed_files']} files, {settings['max_changed_lines']} lines)"))

    live = observation["live"]
    if live["state"] != "open" or live["merged"]:
        found.append(flag("state", "pr-not-open", "the PR was no longer open when runs were listed"))
    if live["head_sha"] != observation["snapshot_head_sha"]:
        found.append(flag("state", "head-moved", "the PR head moved after the selected snapshot; acquire a new snapshot and list again"))
    if live["head_repository"] != head_repository(summary):
        found.append(flag("state", "head-repository-changed", "the PR's head repository differs from the selected snapshot"))

    age = (timestamp(now()) - timestamp(observation["observed_at"])).total_seconds()
    if not 0 <= age <= max_age:
        found.append(flag("state", "observation-stale", f"runs were listed {int(age)} seconds ago, beyond the {max_age}-second limit"))

    return found, changed


def run_flags(run, observation, workflows, changed):
    found = []
    if run["event"] != "pull_request":
        found.append(flag("state", "unexpected-event", f"run event is {run['event']!r}, not pull_request"))
    if "action_required" not in (run["status"], run["conclusion"]):
        found.append(flag("state", "not-awaiting", "the run was not awaiting approval when listed"))
    if run["head_repository"] != observation["live"]["head_repository"]:
        found.append(flag("state", "head-repository-changed", "the run's head repository differs from the PR's"))

    if workflows is None:
        found.append(flag("privileged-context", "base-workflows-unavailable", "the base commit's workflows are not in the local checkout, so privileged triggers and runners cannot be checked"))
        return found

    consumers = sorted(name for name, text in workflows.items() if text is None or re.search(r"\b(?:workflow_run|pull_request_target)\b", text))
    if consumers:
        found.append(flag("privileged-context", "privileged-trigger", "base workflows use workflow_run or pull_request_target, which can carry this run's output into a context with secrets", [dict(path=name) for name in consumers]))

    text = workflows.get(run["path"])
    if text is None:
        found.append(flag("privileged-context", "workflow-not-on-base", "the run's workflow file is absent or unreadable at the base commit"))
        return found

    runners = runner_flags(text)
    if runners:
        found.append(flag("privileged-context", "non-hosted-runner", f"the workflow may run on a self-hosted or unrecognized runner: {', '.join(runners)[:200]}", [dict(path=run["path"])]))
    if re.search(r"^\s*environment:", text, re.MULTILINE):
        found.append(flag("privileged-context", "deployment-environment", "the workflow uses a deployment environment", [dict(path=run["path"])]))

    executed = executed_by(text)
    inputs = [dict(path=path) for path in changed if matches(path, executed)]
    if inputs:
        found.append(flag("ci-executes", "workflow-input-changed", "the PR changes files this workflow runs", inputs))

    return found


def flagged_hunks(diff_text, flags):
    """The snapshot's diff hunks behind each flag location, for a person to read: the hunk holding a flagged line, or every hunk of a flagged file. Locations outside the diff, such as base workflows, have none."""
    files = {}
    for entry in parse_diff(diff_text):
        files.setdefault(entry["path"] or entry["old_path"], entry)

    selected = {}
    for value in flags:
        for location in value["locations"]:
            entry = files.get(location.get("path"))
            if entry is None:
                continue

            line = location.get("line")
            # A file with no hunks (binary, mode-only, a pure rename) shows its headers instead, as hunk index -1.
            indexes = [index for index, hunk in enumerate(entry["hunks"]) if line is None or hunk["start"] <= line <= hunk["end"]] if entry["hunks"] else [-1]
            for index in indexes:
                target = selected.setdefault((location["path"], index), dict(rules=set(), lines=set()))
                target["rules"].add(value["rule"])
                if line is not None:
                    target["lines"].add(line)

    hunks, omissions, size = [], [], 0
    for (path, index), target in sorted(selected.items()):
        if len(hunks) == MAX_HUNKS:
            omissions.append(f"{len(selected) - MAX_HUNKS} more flagged hunks are not shown; read the raw diff")
            break

        entry = files[path]
        hunk = entry["hunks"][index] if index >= 0 else dict(header="", start=0, end=-1, lines=[line for line in entry["headers"] if line])
        lines = [line if len(line) <= MAX_HUNK_LINE_CHARS else line[:MAX_HUNK_LINE_CHARS] + " …" for line in hunk["lines"][:MAX_HUNK_LINES]]
        text = "\n".join(lines)
        size += len(text.encode())
        if size > MAX_HUNK_BYTES:
            omissions.append(f"flagged hunks from {path} onward exceed the display limit; read the raw diff")
            break

        truncated = len(lines) < len(hunk["lines"]) or any(len(line) > MAX_HUNK_LINE_CHARS for line in hunk["lines"][:MAX_HUNK_LINES])
        hunks.append(dict(path=path, header=hunk["header"], start=hunk["start"], end=hunk["end"], text=text, truncated=truncated,
                          rules=sorted(target["rules"]), lines=sorted(target["lines"])))

    return hunks, omissions


def assess(number, host="github.com", max_age=3600, include_hunks=False):
    """Recompute flags offline from the saved observation, its snapshot and the base workflows. The same inputs always give the same checksum."""
    natural(max_age, "maximum age", 1)
    cache = ready_cache(host)
    observation = load(cache, number)
    evidence, summary = selected(number, observation["snapshot_id"], host)
    if evidence["revision"]["head_sha"] != observation["snapshot_head_sha"]:
        raise ValueError("saved observation and its snapshot disagree on the PR head")

    settings, config_checksum = config()
    found, changed = pr_flags(evidence, summary, observation, settings, max_age)
    base = evidence["revision"].get("base_sha")
    workflows = base_workflows(base) if isinstance(base, str) and re.fullmatch(r"[0-9a-f]{40}", base) else None
    runs = []
    for run in observation["runs"]:
        flags = run_flags(run, observation, workflows, changed)
        runs.append(dict(run, state="suspicious" if found or flags else "normal", flags=flags))

    result = dict(repository=REPO, number=number, snapshot_id=observation["snapshot_id"], head_sha=observation["snapshot_head_sha"], base_sha=base,
                  observation=dict(checksum=observation["checksum"], observed_at=observation["observed_at"]),
                  rules=dict(version=RULES_VERSION, config_checksum=config_checksum), pr_flags=found, runs=runs,
                  counts=dict(normal=sum(run["state"] == "normal" for run in runs), suspicious=sum(run["state"] == "suspicious" for run in runs)))
    # A later approval recomputes this checksum and refuses any difference, including an observation that has since gone stale.
    result = dict(result, checksum=digest(canonical(result)), requests=0)
    if include_hunks:
        diff = evidence["data"].get("diff")
        result["hunks"], result["hunk_omissions"] = flagged_hunks(diff, found + [value for run in runs for value in run["flags"]]) if isinstance(diff, str) else ([], ["the snapshot has no diff text"])

    return result
