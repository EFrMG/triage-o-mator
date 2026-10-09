#!/usr/bin/env python3
"""Read-only verification of one seeded private evaluation run."""

import argparse
import base64
import hashlib
import json
from pathlib import Path
import subprocess
from urllib.parse import quote

import seed


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--run-id", required=True)
    args = parser.parse_args()

    run_dir = seed.RECORDS / "runs" / args.run_id
    preview = json.loads((run_dir / "preview.json").read_text())
    state = json.loads((run_dir / "seed-state.json").read_text())
    repo = preview["repository"]

    if state["repository"] != repo or state["plan_sha256"] != preview["plan_sha256"] or not state["install_ready"]:
        raise RuntimeError("Seed state does not match the approved completed preview")

    info = seed.api("GET", f"repos/{repo}")
    actions = seed.api("GET", f"repos/{repo}/actions/permissions")
    base = seed.api("GET", f"repos/{repo}/git/ref/heads/master")
    setup_state_path = run_dir / "dependabot-disable-state.json"
    remote_base = preview["push_base_sha"]

    if setup_state_path.exists():
        setup = json.loads(setup_state_path.read_text())
        remote_base = setup["new_master"]
        comparison = seed.api("GET", f"repos/{repo}/compare/{setup['old_master']}...{setup['new_master']}")

        if setup["old_master"] != preview["push_base_sha"] or len(comparison["files"]) != 1 or comparison["files"][0]["filename"] != ".github/dependabot.yml" or comparison["files"][0]["status"] != "removed":
            raise RuntimeError("Private setup commit changed more than Dependabot configuration")

    if not info["private"] or info["full_name"] != repo or info["default_branch"] != "master" or actions["enabled"] or base["object"]["sha"] != remote_base:
        raise RuntimeError("Remote repository, Actions or pinned base differs from the preview")

    listed_issues = {item["number"]: item for item in seed.pages(repo, "issues")}
    listed_prs = {item["number"]: item for item in seed.pages(repo, "pulls")}
    correction_state_path = run_dir / "conversation-correction-state.json"
    corrected = {}
    preferences = {}

    if correction_state_path.exists():
        correction = json.loads((run_dir / "conversation-correction-preview.json").read_text())
        correction_state = json.loads(correction_state_path.read_text())

        if correction_state.get("plan_sha256") != correction["plan_sha256"] or not correction_state.get("complete"):
            raise RuntimeError("Discussion correction state is incomplete or changed")

        corrected = {operation["number"]: operation for operation in correction["operations"]}

    preference_state_path = run_dir / "preference-correction-state.json"

    if preference_state_path.exists():
        preference = json.loads((run_dir / "preference-correction-preview.json").read_text())
        preference_state = json.loads(preference_state_path.read_text())

        if preference_state.get("plan_sha256") != preference["plan_sha256"] or not preference_state.get("complete"):
            raise RuntimeError("Preference correction state is incomplete or changed")

        preferences = {operation["number"]: operation for operation in preference["operations"]}

    expected_numbers = set()
    verified_prs = 0
    expected_issue_count = sum(operation["kind"] == "issue" for operation in preview["operations"])
    expected_pr_count = sum(operation["kind"] == "pr" for operation in preview["operations"])
    seeded_followups = {state["items"][f"issue:{followup['item_id']}"]: followup for followup in preview.get("followup_comments", [])}

    for operation in preview["operations"]:
        key = f"{operation['kind']}:{operation['id']}"
        number = state["items"][key]
        expected_numbers.add(number)
        item = listed_issues[number]

        expected = corrected.get(number, preferences.get(number, operation))

        if item["title"] != expected["title"] or item["body"] != expected["body"] or item["state"] != "open":
            raise RuntimeError(f"Remote item {key} differs from its exact preview")

        if number in corrected:
            comments = seed.api("GET", f"repos/{repo}/issues/{number}/comments?per_page=100")

            if len(comments) != 1 or comments[0]["body"] != corrected[number]["comment"]:
                raise RuntimeError(f"Corrected issue #{number} lacks its exact follow-up")

        if number in seeded_followups:
            followup = seeded_followups[number]
            comments = seed.api("GET", f"repos/{repo}/issues/{number}/comments?per_page=100")

            if len(comments) != 1 or comments[0]["body"] != followup["body"] or comments[0]["id"] != state["comments"][f"issue:{followup['item_id']}"]:
                raise RuntimeError(f"Seeded issue #{number} lacks its exact follow-up")

        if operation["kind"] != "pr":
            continue

        pull = listed_prs[number]

        if pull["head"]["ref"] != operation["branch"] or pull["base"]["ref"] != operation["base"]:
            raise RuntimeError(f"PR branch or base differs for {key}")

        files = seed.api("GET", f"repos/{repo}/pulls/{number}/files?per_page=100")

        expected_status = "modified" if "base_content_sha256" in operation else "added"

        if len(files) != 1 or files[0]["filename"] != operation["path"] or files[0]["status"] != expected_status:
            raise RuntimeError(f"PR {key} does not have its one expected {expected_status} file")

        if "base_content_sha256" in operation and hashlib.sha256((seed.SOURCE_CLONE / operation["path"]).read_bytes()).hexdigest() != operation["base_content_sha256"]:
            raise RuntimeError(f"PR {key} does not bind the pinned base file")

        path = quote(operation["path"], safe="/")
        branch = quote(operation["branch"], safe="")
        content = seed.api("GET", f"repos/{repo}/contents/{path}?ref={branch}")
        actual = base64.b64decode(content["content"]).decode("utf-8")

        if actual != operation["content"]:
            raise RuntimeError(f"PR {key} file content differs from its preview")

        verified_prs += 1

    if not expected_numbers.issubset(listed_issues):
        raise RuntimeError("Remote repository is missing a seeded item")

    extra_items = [item for number, item in listed_issues.items() if number not in expected_numbers]

    if any(item["user"]["login"] != "dependabot[bot]" or item["state"] != "closed" for item in extra_items):
        raise RuntimeError("Remote repository has an open or unexpected extra item")

    if len(listed_prs) != expected_pr_count + len(extra_items):
        raise RuntimeError("Remote repository PR list differs from seeded and closed bot items")

    local_clone = Path(preview["clone_path"])
    install = Path(preview["install_path"])
    head = subprocess.run(["git", "-C", str(local_clone), "rev-parse", "HEAD"], text=True, capture_output=True, check=True).stdout.strip()
    dirty = subprocess.run(["git", "-C", str(local_clone), "status", "--porcelain"], text=True, capture_output=True, check=True).stdout.strip()
    selected_repo = (install / "config" / "repo").read_text().strip()
    ledger_path = install / "data" / repo / "ledger.jsonl"
    rows = [json.loads(line) for line in ledger_path.read_text().splitlines() if line]
    ledger_keys = {(row["kind"], row["number"]) for row in rows}
    expected_keys = {(operation["kind"], state["items"][f"{operation['kind']}:{operation['id']}"]) for operation in preview["operations"]}

    if head not in {preview["push_base_sha"], remote_base} or dirty or selected_repo != repo or ledger_keys != expected_keys:
        raise RuntimeError("Local clone or install does not match the seeded repository and item set")

    policy = subprocess.run([str(install / "bin" / "action-policy"), "status", "--expected-repo", repo], text=True, capture_output=True, check=True)
    policy_data = json.loads(policy.stdout)

    if any(action["mode"] != "stage" for action in policy_data["actions"]):
        raise RuntimeError("Fresh install has a non-staged action policy")

    seeded_preference_requests = sum(operation["kind"] == "issue" and operation["id"].endswith("-distinct") for operation in preview["operations"])
    report = {"repository": repo, "plan_sha256": preview["plan_sha256"], "local_checkout": head, "pinned_source": preview["push_base_sha"], "remote_base": remote_base, "private": True, "actions_disabled": True, "remote_issues": expected_issue_count, "remote_prs": verified_prs, "closed_bot_prs_excluded": len(extra_items), "exact_titles_and_bodies": len(expected_numbers), "seeded_followups": len(seeded_followups), "seeded_preference_requests": seeded_preference_requests, "legacy_conversation_corrections": len(corrected), "legacy_preference_corrections": len(preferences), "single_file_pr_diffs": verified_prs, "ledger_rows": len(rows), "local_clone_clean": True, "action_policy": "stage"}
    (run_dir / "audit.json").write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(report, indent=2))


if __name__ == "__main__":
    main()
