#!/usr/bin/env python3
"""Preview and explicitly seed isolated private GitHub evaluation fixtures."""

import argparse
import base64
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time
from urllib.parse import quote


HERE = Path(__file__).resolve().parent
SPEC_PATH = HERE / "fixtures.json"
CODE_ROOT = HERE.parents[1]
# This directory is the shared kit. Run records, withheld guides, unpublished fixtures and archives are local and stay out of it.
# Run "0" seeds the base topic templates alone, with no run fixture, as a setup smoke check. Every other ID needs a reviewed fixture.
BASE_RUN_ID = "0"
RECORDS = Path(os.environ.get("TRIAGE_EVAL_RECORDS") or CODE_ROOT / "DOCS" / "triage-playbook-evals").resolve() / "seed"
SOURCE_CLONE = Path(os.environ.get("TRIAGE_EVAL_SOURCE_CLONE", "/workspace/lazygit")).resolve()
TEST_CLONE = Path(os.environ.get("TRIAGE_EVAL_CLONE_PATH", "/workspace/lazygit-clone")).resolve()


ISSUE_CASES = (
    ("clear_bug", "Unexpected {feature} result", "After {trigger}, {observed}. Expected result: {expected}. I can repeat this sequence and see the same result. I was looking at {source} while trying to understand it."),
    ("duplicate", "Unexpected {feature} result", "After {trigger}, {observed}. Expected result: {expected}. It happens each time I repeat those steps on the pinned checkout."),
    ("distinct", "Feature request for {feature}", "After {trigger}, the action finishes and I can continue working. I would like {preference}. Is that an option maintainers would consider?"),
    ("no_fit", "Add a maintainer policy gate for {feature}", "Could maintainers {policy_request}? I do not know whether this belongs in the release process or in contributor guidance."),
    ("resolved_close", "Resolved {feature} question", "I asked what should happen after {trigger}. The documentation at {source} answered it for my case, and I confirmed the behavior I needed. Thanks for the help; my question is resolved."),
    ("already_answered", "Question about {feature} behavior", "I am trying to understand what should happen after {trigger}. Is it expected that {expected}? I was reading {source}, but I may be missing the relevant passage."),
    ("needs_question", "Sparse {feature} failure report", "Something failed while I was using {feature}. I have not captured the command or key sequence immediately before it happened, and I do not have the output yet."),
    ("stale_preview", "Question about {feature}", "After {trigger}, I see that {observed}. Is it intended that {expected}?"),
    ("unassessed", "Possible {feature} problem", "Something seems wrong with {feature}. I will try to collect a clearer example when I can."),
)

PR_CASES = (
    ("survivor", "Document {feature} trigger and expected result", "This adds a short note for {feature} covering the trigger, reported result and expected result. Please review the wording against {source}.", "# {feature}\n\nTrigger: {trigger}.\n\nReported result: {observed}.\n\nExpected result: {expected}.\n\nRelated source area: {source}.\n"),
    ("superseded", "Document the {feature} symptom", "This adds a short note about the result reported for {feature}. I left out reproduction and expected-behavior details because I did not have them when I drafted it.", "# {feature}\n\nReported result: {observed}.\n"),
)


def api(method, path, body=None, missing_ok=False):
    command = ["gh", "api", "--method", method, path]

    if body is not None:
        command.extend(["--input", "-"])

    if method != "GET":
        time.sleep(1)

    result = subprocess.run(command, input=json.dumps(body) if body is not None else None, text=True, capture_output=True, check=False)

    if result.returncode:
        if missing_ok and ("(HTTP 404)" in result.stderr or ("/git/ref/" in path and "(HTTP 409)" in result.stderr)):
            return None

        raise RuntimeError(f"GitHub {method} {path} failed: {result.stderr.strip()}")

    return json.loads(result.stdout) if result.stdout.strip() else {}


def pages(repo, kind):
    items = []
    page = 1

    while True:
        batch = api("GET", f"repos/{repo}/{kind}?state=all&per_page=100&page={page}")

        if not isinstance(batch, list):
            raise RuntimeError(f"Unexpected {kind} list reply")

        items.extend(batch)

        if len(batch) < 100:
            return items

        page += 1


def title(marker, item):
    return f"[{marker}/{item['public_id']}] {item['title']}"


def pr_files(item):
    return item["files"] if "files" in item else [{key: item[key] for key in ("path", "content", "base_content_sha256") if key in item}]


def expanded_pr_files(pr):
    edits = pr.get("files", [pr])

    if not isinstance(edits, list) or not 1 <= len(edits) <= 20 or ("files" in pr and any(key in pr for key in ("path", "append", "replace"))):
        raise RuntimeError("PR needs one to twenty file edits, without mixed legacy fields")

    files = []
    seen = set()

    for edit in edits:
        name = edit.get("path")

        if not isinstance(name, str) or not name or "\\" in name or name.startswith("/") or any(part in ("", ".", "..", ".git") for part in name.split("/")) or name in seen:
            raise RuntimeError("PR file paths must be unique repository-relative paths")

        path = SOURCE_CLONE / name

        if not path.is_file() or path.is_symlink() or not path.resolve().is_relative_to(SOURCE_CLONE.resolve()):
            raise RuntimeError(f"PR edit is not a regular pinned source file: {name}")

        seen.add(name)
        base_content = path.read_bytes().decode("utf-8")

        if ("append" in edit) == ("replace" in edit):
            raise RuntimeError(f"PR edit needs exactly one append or replace: {name}")

        if "append" in edit:
            new_content = base_content + edit["append"]

        else:
            old = edit["replace"]["old"]

            if not old or base_content.count(old) != 1:
                raise RuntimeError(f"PR replacement is not unique in {path}")

            new_content = base_content.replace(old, edit["replace"]["new"], 1)

        if new_content == base_content:
            raise RuntimeError(f"PR has no operative diff: {name}")

        files.append({"path": name, "content": new_content, "base_content_sha256": hashlib.sha256(base_content.encode()).hexdigest()})

    return files


def expanded(spec):
    issues = []
    pulls = []

    for topic_index, topic in enumerate(spec["topics"], 1):
        issue_cases = [(case_id, case_title.format_map(topic), body.format_map(topic)) for case_id, case_title, body in ISSUE_CASES]
        pr_cases = [(case_id, case_title.format_map(topic), body.format_map(topic), content.format_map(topic), f"docs/{spec['marker']}-topic{topic_index:02d}-behavior.md", None) for case_id, case_title, body, content in PR_CASES]

        if extra := spec.get("expanded_cases"):
            cases = extra["issues"][topic["id"]]
            issue_cases.extend((case_id, cases[f"{prefix}_title"], cases[f"{prefix}_body"]) for case_id, prefix in (("alt_duplicate", "other_words"), ("same_title_distinct", "different_cause"), ("unresolved_pair", "unresolved")))
            pr = extra["prs"][topic["id"]]
            files = expanded_pr_files(pr)
            first = files[0]
            pr_cases.append(("extra_pr", pr["title"], pr["body"], first["content"], first["path"], first["base_content_sha256"]))

        issue_cases.sort(key=lambda case: hashlib.sha256(f"{spec['marker']}:{topic['id']}:issue:{case[0]}".encode()).digest())
        pr_cases.sort(key=lambda case: hashlib.sha256(f"{spec['marker']}:{topic['id']}:pr:{case[0]}".encode()).digest())

        for case_index, (case_id, case_title, body) in enumerate(issue_cases, 1):
            issues.append({"id": f"{topic['id']}-{case_id}", "public_id": f"i{topic_index:02d}{case_index:02d}", "title": case_title, "body": body})

        for case_index, (case_id, case_title, body, content, path, base_content_sha256) in enumerate(pr_cases, 1):
            item_id = f"{topic['id']}-{case_id}"
            public_id = f"p{topic_index:02d}{case_index:02d}"
            item = {"id": item_id, "public_id": public_id, "title": case_title, "body": body, "content": content, "branch": f"{spec['marker']}-{public_id}", "path": path}

            if base_content_sha256:
                item["base_content_sha256"] = base_content_sha256

            if case_id == "extra_pr" and "files" in pr:
                item = {key: value for key, value in item.items() if key not in ("path", "content", "base_content_sha256")}
                item["files"] = files

            pulls.append(item)

    extra = spec.get("expanded_cases", {})

    for kind, items, field in (("issue", issues, "extra_issues"), ("pr", pulls, "extra_prs")):
        additions = extra.get(field, [])

        if not isinstance(additions, list) or len(additions) > 99:
            raise RuntimeError(f"{field} must be a list of at most 99 items")

        seen = {item["id"] for item in items}

        additions = sorted(additions, key=lambda item: hashlib.sha256(f"{spec['marker']}:{kind}:{item.get('id', '')}".encode()).digest())

        for index, addition in enumerate(additions, 1):
            item_id = addition.get("id", "")

            if not re.fullmatch(r"[a-z][a-z0-9-]{0,79}", item_id) or item_id in seen or not all(isinstance(addition.get(key), str) and addition[key].strip() for key in ("title", "body")):
                raise RuntimeError(f"Invalid or duplicate supplemental {kind} item")

            seen.add(item_id)
            public_id = f"{kind[0]}{len(spec['topics']) + 1:02d}{index:02d}"
            item = {key: addition[key] for key in ("id", "title", "body")}
            item["public_id"] = public_id

            if kind == "pr":
                item.update(files=expanded_pr_files(addition), branch=f"{spec['marker']}-{public_id}")

            items.append(item)

    return issues, pulls


def followup_key(followup):
    return f"{followup.get('kind', 'issue')}:{followup['item_id']}"


def followup_comments(spec):
    comments = [{"item_id": f"{topic['id']}-already_answered", "body": f"Update: I found the relevant explanation in {topic['source']} and checked the behavior on my setup. That answers my question for now; thanks."} for topic in spec["topics"]]

    if extra := spec.get("expanded_cases"):
        comments.extend(extra.get("comments", []))

    issues, pulls = expanded(spec)
    allowed = {f"{kind}:{item['id']}" for kind, items in (("issue", issues), ("pr", pulls)) for item in items}
    seen = set()

    for comment in comments:
        key = followup_key(comment)

        if key not in allowed or key in seen or not isinstance(comment.get("body"), str) or not comment["body"].strip():
            raise RuntimeError("Follow-up must name one unique seeded issue or PR and have text")

        seen.add(key)

    return comments


def exact_match(items, wanted_title, wanted_body, branch=None, base=None):
    fixture_prefix = wanted_title.split("]", 1)[0] + "]"
    matches = [item for item in items if item.get("title", "").startswith(fixture_prefix)]

    if len(matches) > 1:
        raise RuntimeError(f"Multiple remote fixtures have title {wanted_title!r}")

    if not matches:
        return None

    item = matches[0]

    if item.get("title") != wanted_title or item.get("body") != wanted_body:
        raise RuntimeError(f"Existing fixture #{item['number']} has changed title or body: {wanted_title}")

    if branch is not None and (item.get("head", {}).get("ref") != branch or item.get("base", {}).get("ref") != base):
        raise RuntimeError(f"Existing PR #{item['number']} has changed branch or base: {wanted_title}")

    return item


def clone_path(spec):
    return TEST_CLONE


def validate_previous_archive(spec, archive_path):
    archive = Path(archive_path).resolve()
    records = RECORDS.parent

    if not archive.is_file() or records not in archive.parents or not archive.name.endswith(".tar.gz"):
        raise RuntimeError(f"Previous archive must be a verified tar.gz inside {records}")

    sidecar = Path(str(archive) + ".sha256")
    recorded = sidecar.read_text().split()[0]
    actual = hashlib.sha256(archive.read_bytes()).hexdigest()

    if recorded != actual:
        raise RuntimeError("Previous archive checksum differs")

    with tarfile.open(archive, "r:gz") as bundle:
        manifest = json.load(bundle.extractfile("./manifest.json"))
        archived_head = bundle.extractfile("./local-checkout-head.txt").read().decode().strip()

    if manifest.get("repository") != spec["repository"] or manifest.get("install") != "triage-o-mator":
        raise RuntimeError("Previous archive does not contain this repository and install")

    return archive, archived_head, manifest


def verify_old_clone_against_archive(spec, archive_path):
    local_clone = clone_path(spec)
    archive, archived_head, manifest = validate_previous_archive(spec, archive_path)
    checks = ((["rev-parse", "HEAD"], archived_head), (["remote", "get-url", "origin"], f"https://github.com/{spec['repository']}.git"), (["status", "--porcelain", "--untracked-files=no"], ""))

    for arguments, expected in checks:
        result = subprocess.run(["git", "-C", str(local_clone), *arguments], text=True, capture_output=True, check=True)

        if result.stdout.strip() != expected:
            raise RuntimeError(f"Old clone differs from archive: {' '.join(arguments)}")

    install = local_clone / "triage-o-mator"
    expected = {item["path"].removeprefix("triage-o-mator/"): item for item in manifest["files"] if item["path"].startswith("triage-o-mator/")}
    observed = {}

    for path in install.rglob("*"):
        name = str(path.relative_to(install))

        if path.is_symlink():
            observed[name] = {"symlink": os.readlink(path)}

        elif path.is_file():
            observed[name] = {"sha256": hashlib.sha256(path.read_bytes()).hexdigest()}

    if set(observed) != set(expected) or any(any(observed[name].get(key) != value for key, value in expected[name].items() if key in ("sha256", "symlink")) for name in expected):
        raise RuntimeError(f"Old install changed since {archive}")


def verify_source(spec):
    checks = ((["rev-parse", "HEAD"], spec["base_sha"]), (["remote", "get-url", "origin"], "https://github.com/jesseduffield/lazygit.git"), (["status", "--porcelain", "--untracked-files=no"], ""))

    for arguments, expected in checks:
        result = subprocess.run(["git", "-C", str(SOURCE_CLONE), *arguments], text=True, capture_output=True, check=True)

        if result.stdout.strip() != expected:
            raise RuntimeError(f"Pinned public source check failed: {' '.join(arguments)}")


def verify_repo(spec):
    repo = spec["repository"]
    identity = api("GET", "user")

    if identity.get("login", "").lower() != spec["owner"].lower():
        raise RuntimeError("Authenticated GitHub account is not the private fixture owner")

    info = api("GET", f"repos/{repo}", missing_ok=True)

    if info is None:
        return None

    if info.get("full_name", "").lower() != repo.lower() or info.get("private") is not True or info.get("archived") or not info.get("permissions", {}).get("push") or info.get("description") != spec["description"]:
        raise RuntimeError("Existing repository is not this run's private fixture repository")

    actions = api("GET", f"repos/{repo}/actions/permissions")
    ref = api("GET", f"repos/{repo}/git/ref/heads/{spec['base_branch']}", missing_ok=True)

    if ref is not None and info.get("default_branch") != spec["base_branch"]:
        raise RuntimeError("Private repository default branch changed")

    if ref is not None and actions.get("enabled") is not False:
        raise RuntimeError("GitHub Actions must be disabled before a fixture branch exists")

    private_base = ref.get("object", {}).get("sha") if ref is not None else None

    if private_base is not None and private_base != spec["base_sha"]:
        comparison = api("GET", f"repos/{repo}/compare/{spec['base_sha']}...{private_base}")

        if comparison.get("ahead_by") != 1 or len(comparison.get("files", [])) != 1 or comparison["files"][0].get("filename") != ".github/dependabot.yml" or comparison["files"][0].get("status") != "removed":
            raise RuntimeError("Private base differs beyond the one Dependabot setup deletion")

    return {**info, "_base_ready": ref is not None, "_base_sha": private_base}


def verify_clone(spec):
    local_clone = clone_path(spec)
    checks = ((["rev-parse", "HEAD"], spec["base_sha"]), (["remote", "get-url", "origin"], f"https://github.com/{spec['repository']}.git"), (["status", "--porcelain", "--untracked-files=no"], ""))

    for arguments, expected in checks:
        result = subprocess.run(["git", "-C", str(local_clone), *arguments], text=True, capture_output=True, check=True)

        if result.stdout.strip() != expected:
            raise RuntimeError(f"Private clone check failed: {' '.join(arguments)}")


def planned(spec, issues, pulls):
    marker = spec["marker"]
    repo = spec["repository"]
    entries = []
    issue_cases, pr_cases = expanded(spec)

    for item in issue_cases:
        wanted_title = title(marker, item)
        existing = exact_match(issues, wanted_title, item["body"])
        entries.append({"kind": "issue", "id": item["id"], "target": f"{repo}/issues", "existing_number": existing["number"] if existing else None, "title": wanted_title, "body": item["body"]})

    for item in pr_cases:
        wanted_title = title(marker, item)
        existing = exact_match(pulls, wanted_title, item["body"], item["branch"], spec["base_branch"])
        entry = {"kind": "pr", "id": item["id"], "public_id": item["public_id"], "target": f"{repo}/pulls", "existing_number": existing["number"] if existing else None, "title": wanted_title, "body": item["body"], "branch": item["branch"], "base": spec["base_branch"]}
        entry.update({"files": item["files"]} if "files" in item else {"path": item["path"], "content": item["content"]})

        if "base_content_sha256" in item:
            entry["base_content_sha256"] = item["base_content_sha256"]

        entries.append(entry)

    return entries


def state_for(path, plan_hash, repo):
    if not path.exists():
        return {"plan_sha256": plan_hash, "repository": repo, "items": {}, "comments": {}, "install_ready": False}

    state = json.loads(path.read_text())

    if state.get("plan_sha256") != plan_hash or state.get("repository") != repo:
        raise RuntimeError("Saved seed state belongs to another plan or repository")

    return state


def save_state(path, state):
    path.parent.mkdir(parents=True, exist_ok=True)

    with tempfile.NamedTemporaryFile("w", dir=path.parent, prefix=".seed-state-", suffix=".json", delete=False) as handle:
        json.dump(state, handle, indent=2, sort_keys=True)
        handle.write("\n")
        temporary = Path(handle.name)

    os.replace(temporary, path)


def ensure_install(spec):
    repo = spec["repository"]
    local_clone = clone_path(spec)
    install = local_clone / "triage-o-mator"
    if install.exists():
        marker_path = install / ".triage-install.json"
        marker = json.loads(marker_path.read_text())

        if marker.get("repo") != repo or marker.get("mode") != "solo" or Path(marker.get("tool", "")).resolve() != CODE_ROOT:
            raise RuntimeError("Private install marker did not match the expected repository, mode and tool")

        if (install / "config" / "repo").read_text().strip() != repo or (install / "bin").resolve() != CODE_ROOT / "bin":
            raise RuntimeError("Private install paths did not match the expected repository and checkout")

        return

    command = [str(CODE_ROOT / "bin" / "install-to"), str(local_clone), "--repo", repo, "--solo", "--no-agents-md", "--yes"]
    subprocess.run(command, check=True)


def ledger_difference(spec, state):
    ledger = clone_path(spec) / "triage-o-mator" / "data" / spec["repository"] / "ledger.jsonl"
    observed = {(row["kind"], row["number"]) for row in (json.loads(line) for line in ledger.read_text().splitlines() if line)} if ledger.exists() else set()
    expected = {(key.split(":", 1)[0], number) for key, number in state["items"].items()}
    return expected - observed, observed - expected


def ensure_repo_base(spec, existing):
    repo = spec["repository"]

    if existing is None:
        api("POST", "user/repos", {"name": repo.split("/", 1)[1], "description": spec["description"], "private": True, "has_issues": True, "auto_init": False})

    api("PUT", f"repos/{repo}/actions/permissions", {"enabled": False})
    ref = api("GET", f"repos/{repo}/git/ref/heads/{spec['base_branch']}", missing_ok=True)

    if ref is None:
        target = f"https://github.com/{repo}.git"
        command = ["git", "-c", "core.hooksPath=/dev/null", "-C", str(SOURCE_CLONE), "push", target, f"{spec['base_sha']}:refs/heads/{spec['base_branch']}"]
        result = subprocess.run(command, text=True, capture_output=True, check=False)

        if result.returncode:
            raise RuntimeError(f"Pinned-base push failed: {result.stderr.strip()}")

    elif ref.get("object", {}).get("sha") != spec["base_sha"]:
        verified = verify_repo(spec)

        if verified is None:
            raise RuntimeError("Existing private repository could not be verified")

    info = api("GET", f"repos/{repo}")

    if info.get("default_branch") != spec["base_branch"]:
        api("PATCH", f"repos/{repo}", {"default_branch": spec["base_branch"]})

    path = ".github/dependabot.yml"
    config = api("GET", f"repos/{repo}/contents/{path}?ref={spec['base_branch']}", missing_ok=True)

    if config is not None:
        actual = base64.b64decode(config["content"])

        if actual != (SOURCE_CLONE / path).read_bytes():
            raise RuntimeError("Private Dependabot config differs from the pinned source")

        removed = api("DELETE", f"repos/{repo}/contents/{path}", {"message": "Disable Dependabot version updates in private evaluation fixture", "sha": config["sha"], "branch": spec["base_branch"]})
        state_path = RECORDS / "runs" / spec["run_id"] / "dependabot-disable-state.json"
        state_path.parent.mkdir(parents=True, exist_ok=True)
        state_path.write_text(json.dumps({"repository": repo, "old_master": spec["base_sha"], "new_master": removed["commit"]["sha"], "removed_path": path}, indent=2) + "\n")

    verified = verify_repo(spec)

    if verified is None:
        raise RuntimeError("Created private repository could not be verified")

    return verified["_base_sha"]


def ensure_clone(spec, previous_archive):
    local_clone = clone_path(spec)

    if local_clone.exists():
        try:
            verify_clone(spec)
            return
        except (RuntimeError, subprocess.CalledProcessError):
            if not previous_archive:
                raise RuntimeError("Existing test clone requires a verified previous archive")

            verify_old_clone_against_archive(spec, previous_archive)
            shutil.rmtree(local_clone)

    subprocess.run(["git", "clone", f"https://github.com/{spec['repository']}.git", str(local_clone)], check=True)
    verify_clone(spec)


def existing_branch_content(repo, item):
    branch = quote(item["branch"], safe="")

    for file in pr_files(item):
        path = quote(file["path"], safe="/")
        content = api("GET", f"repos/{repo}/contents/{path}?ref={branch}", missing_ok=True)

        if content is None or content.get("encoding") != "base64":
            raise RuntimeError(f"Existing branch {item['branch']} lacks its expected fixture file")

        actual = base64.b64decode(content["content"]).decode("utf-8")

        if actual != file["content"]:
            raise RuntimeError(f"Existing branch {item['branch']} has changed fixture content")


def ensure_branch(spec, item):
    repo = spec["repository"]
    branch = item["branch"]
    files = pr_files(item)

    for file in files:
        if "base_content_sha256" in file:
            actual = hashlib.sha256((SOURCE_CLONE / file["path"]).read_bytes()).hexdigest()

            if actual != file["base_content_sha256"]:
                raise RuntimeError(f"Pinned base content changed for {file['path']}")

    ref = api("GET", f"repos/{repo}/git/ref/heads/{quote(branch, safe='')}", missing_ok=True)

    if ref is not None:
        existing_branch_content(repo, item)
        return ref["object"]["sha"]

    base = spec["base_sha"]
    base_commit = api("GET", f"repos/{repo}/git/commits/{base}")
    entries = []

    for file in files:
        blob = api("POST", f"repos/{repo}/git/blobs", {"content": file["content"], "encoding": "utf-8"})
        entries.append({"path": file["path"], "mode": "100644", "type": "blob", "sha": blob["sha"]})

    tree = api("POST", f"repos/{repo}/git/trees", {"base_tree": base_commit["tree"]["sha"], "tree": entries})
    commit = api("POST", f"repos/{repo}/git/commits", {"message": f"Seed {spec['marker']} {item['public_id']} fixture change", "tree": tree["sha"], "parents": [base]})
    api("POST", f"repos/{repo}/git/refs", {"ref": f"refs/heads/{branch}", "sha": commit["sha"]})

    return commit["sha"]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--expected-repo", required=True)
    parser.add_argument("--run-id", required=True, help="new short lowercase run identifier; use a different ID for each fresh test run")
    parser.add_argument("--previous-archive", help="verified archive of the old private fixture install to replace")
    parser.add_argument("--preview-after-reset", action="store_true", help="preview a fresh run while the archived previous remote still exists; never applies writes")
    parser.add_argument("--apply", action="store_true", help="create missing fixtures after exact plan review")
    parser.add_argument("--plan-sha256", help="SHA-256 printed by the matching preview; required with --apply")
    args = parser.parse_args()

    if not re.fullmatch(r"[a-z0-9]{1,12}", args.run_id):
        parser.error("--run-id must be 1–12 lowercase letters or digits")

    spec = json.loads(SPEC_PATH.read_text())

    if override := os.environ.get("TRIAGE_EVAL_REPOSITORY"):
        if not re.fullmatch(r"[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+", override):
            parser.error("TRIAGE_EVAL_REPOSITORY must be OWNER/REPO")

        spec["owner"], spec["repository"] = override.split("/", 1)[0], override

    # A new run's fixture stays in the local records until the run is over; a published copy sits beside this script.
    fixture_path = RECORDS / f"{args.run_id}.json"

    if not fixture_path.exists():
        fixture_path = HERE / f"{args.run_id}.json"

    if fixture_path.exists():
        fixture = json.loads(fixture_path.read_text())

        public_scope = fixture.get("public_read_scope", {})
        briefing = fixture.get("briefing_batches", {})

        if not isinstance(public_scope, dict) or not all(public_scope.get(field) for field in ("repository", "corpus_id", "inventory_snapshot", "source_commit", "mode")):
            parser.error(f"{fixture_path} needs an explicit public_read_scope")

        if not isinstance(briefing, dict) or not all(isinstance(briefing.get(kind), list) and briefing[kind] and all(isinstance(size, int) and 0 < size <= 40 for size in briefing[kind]) for kind in ("issue", "pr")):
            parser.error(f"{fixture_path} needs bounded issue and PR briefing_batches")

        if public_scope["mode"] != "fixed-snapshot offline":
            parser.error("public_read_scope mode must be fixed-snapshot offline")

        spec["expanded_cases"] = fixture

    elif args.run_id != BASE_RUN_ID:
        parser.error(f"No fixture specification at {RECORDS / (args.run_id + '.json')}; create and review it before planning a new run")

    spec["run_id"] = args.run_id
    spec["marker"] = f"{spec['marker']}-{args.run_id}"
    spec["description"] = f"Private lazygit review workspace {spec['marker']}"

    if args.expected_repo != spec["repository"]:
        parser.error("--expected-repo does not match the fixture repository")

    if args.apply != bool(args.plan_sha256):
        parser.error("--apply and --plan-sha256 must be supplied together")

    if args.preview_after_reset and (args.apply or not args.previous_archive):
        parser.error("--preview-after-reset requires a previous archive and dry-run mode")

    verify_source(spec)
    previous_archive = None
    previous_archive_sha256 = None

    state_path = RECORDS / "runs" / args.run_id / "seed-state.json"

    if clone_path(spec).exists() and not args.previous_archive and not state_path.exists():
        parser.error(f"--previous-archive is required while {clone_path(spec)} exists")

    if args.previous_archive:
        previous_archive, _, _ = validate_previous_archive(spec, args.previous_archive)
        previous_archive_sha256 = hashlib.sha256(previous_archive.read_bytes()).hexdigest()

    if args.preview_after_reset:
        verify_old_clone_against_archive(spec, previous_archive)
        identity = api("GET", "user")
        current = api("GET", f"repos/{spec['repository']}")
        current_actions = api("GET", f"repos/{spec['repository']}/actions/permissions")

        if identity.get("login", "").lower() != spec["owner"].lower() or current.get("full_name", "").lower() != spec["repository"].lower() or current.get("private") is not True or current.get("default_branch") != spec["base_branch"] or current_actions.get("enabled") is not False:
            raise RuntimeError("Existing remote is not the archived private fixture")

        subprocess.run([sys.executable, str(HERE / "verify_live_archive.py"), str(previous_archive), spec["repository"]], check=True, capture_output=True, text=True)

    existing_repo = None if args.preview_after_reset else verify_repo(spec)
    repo = spec["repository"]
    issues = [item for item in pages(repo, "issues") if "pull_request" not in item] if existing_repo and existing_repo["_base_ready"] else []
    pulls = pages(repo, "pulls") if existing_repo and existing_repo["_base_ready"] else []
    entries = planned(spec, issues, pulls)

    if "expanded_cases" in spec:
        briefing = spec["expanded_cases"]["briefing_batches"]
        counts = {kind: sum(entry["kind"] == kind for entry in entries) for kind in ("issue", "pr")}

        if any(sum(briefing[kind]) != counts[kind] for kind in counts):
            parser.error(f"{fixture_path} briefing_batches do not cover the planned issue and PR counts")

    followups = followup_comments(spec)
    intent = [{key: value for key, value in entry.items() if key != "existing_number"} for entry in entries]
    plan_hash = hashlib.sha256(json.dumps({"spec": spec, "operations": intent, "followup_comments": followups, "create_private_repo": repo, "disable_dependabot_config": ".github/dependabot.yml", "replace_clone": str(clone_path(spec)), "previous_archive": str(previous_archive) if previous_archive else None, "previous_archive_sha256": previous_archive_sha256, "install_path": str(clone_path(spec) / "triage-o-mator")}, sort_keys=True, separators=(",", ":")).encode()).hexdigest()

    if args.apply and args.plan_sha256 != plan_hash:
        parser.error("plan hash does not match the current generated fixture text")

    if not args.apply:
        preview = {"repository": repo, "run_id": args.run_id, "plan_sha256": plan_hash, "mode": "dry-run", "create_private_repo": existing_repo is None, "disable_actions": True, "disable_dependabot_config": ".github/dependabot.yml", "push_base_sha": spec["base_sha"], "clone_path": str(clone_path(spec)), "previous_archive": str(previous_archive) if previous_archive else None, "previous_archive_sha256": previous_archive_sha256, "install_path": str(clone_path(spec) / "triage-o-mator"), "operations": entries, "followup_comments": followups}

        if "expanded_cases" in spec:
            preview["public_read_scope"] = spec["expanded_cases"]["public_read_scope"]
            preview["briefing_batches"] = spec["expanded_cases"]["briefing_batches"]

        print(json.dumps(preview, indent=2))
        return

    state = state_for(state_path, plan_hash, repo)
    private_base = ensure_repo_base(spec, existing_repo)
    runtime_spec = {**spec, "base_sha": private_base}
    ensure_clone(runtime_spec, previous_archive)

    for entry in entries:
        key = f"{entry['kind']}:{entry['id']}"
        number = entry["existing_number"]
        saved_number = state["items"].get(key)

        if saved_number is not None and saved_number != number:
            raise RuntimeError(f"Saved fixture {key} is absent or has a different remote number; inspect before resuming")

        if number is None and entry["kind"] == "issue":
            created = api("POST", f"repos/{repo}/issues", {"title": entry["title"], "body": entry["body"]})
            number = created["number"]

        elif number is None:
            ensure_branch(runtime_spec, entry)
            created = api("POST", f"repos/{repo}/pulls", {"title": entry["title"], "body": entry["body"], "head": entry["branch"], "base": entry["base"]})
            number = created["number"]

        if entry["kind"] == "pr":
            existing_branch_content(repo, entry)

        state["items"][key] = number
        save_state(state_path, state)
        print(f"{key} -> {repo}#{number}")

    for followup in followups:
        key = followup_key(followup)
        number = state["items"][key]
        comments = api("GET", f"repos/{repo}/issues/{number}/comments?per_page=100")

        if not comments:
            created = api("POST", f"repos/{repo}/issues/{number}/comments", {"body": followup["body"]})
            comment_id = created["id"]

        elif len(comments) == 1 and comments[0]["body"] == followup["body"]:
            comment_id = comments[0]["id"]

        else:
            raise RuntimeError(f"Item #{number} has an unexpected discussion; inspect before resuming")

        saved_comment = state["comments"].get(key)

        if saved_comment is not None and saved_comment != comment_id:
            raise RuntimeError(f"Saved comment for item #{number} differs from the remote discussion")

        state["comments"][key] = comment_id
        save_state(state_path, state)
        print(f"{key} follow-up -> comment {comment_id}")

    ensure_install(spec)
    install = clone_path(spec) / "triage-o-mator"
    missing, unexpected = ledger_difference(spec, state)

    if missing or unexpected or not state["install_ready"]:
        for attempt in range(2):
            if attempt:
                time.sleep(2)

            subprocess.run([str(install / "bin" / "fetch"), "--full"], cwd=install, check=True)
            subprocess.run([str(install / "bin" / "sync")], cwd=install, check=True)
            missing, unexpected = ledger_difference(spec, state)

            if not missing and not unexpected:
                break

        if missing or unexpected:
            raise RuntimeError(f"Fresh ledger coverage differs from seed: missing={sorted(missing)}, unexpected={sorted(unexpected)}")

    open_pulls = [item for item in pages(repo, "pulls") if item["state"] == "open"]
    expected_prs = {number for key, number in state["items"].items() if key.startswith("pr:")}

    if {item["number"] for item in open_pulls} != expected_prs:
        raise RuntimeError("Open PR list contains a missing fixture or an unexpected automation PR")

    if not state["install_ready"]:
        state["install_ready"] = True
        save_state(state_path, state)

    print(json.dumps({"repository": repo, "run_id": args.run_id, "plan_sha256": plan_hash, "created_or_verified": state["items"], "install_ready": state["install_ready"]}, indent=2))


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, ValueError, KeyError, UnicodeDecodeError) as exc:
        print(f"seed failed: {exc}", file=sys.stderr)
        sys.exit(1)
