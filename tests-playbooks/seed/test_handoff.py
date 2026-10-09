#!/usr/bin/env python3
"""Freeze a verified seed as an evaluator record and a self-contained agent handoff."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import subprocess

import seed


def sha256(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def install_signature(install):
    """One digest over every file and link in an install, so a later copy can prove it starts from the audited state."""
    entries = []

    for path in sorted(Path(install).rglob("*")):
        name = str(path.relative_to(install))

        if path.is_symlink():
            entries.append((name, "link", os.readlink(path)))

        elif path.is_file():
            entries.append((name, "file", sha256(path)))

    return hashlib.sha256(json.dumps(entries, separators=(",", ":")).encode()).hexdigest()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--run-id", required=True)
    args = parser.parse_args()
    run_dir = seed.RECORDS / "runs" / args.run_id
    preview = json.loads((run_dir / "preview.json").read_text())
    state = json.loads((run_dir / "seed-state.json").read_text())
    audit = json.loads((run_dir / "audit.json").read_text())
    issue_count = sum(operation["kind"] == "issue" for operation in preview["operations"])
    pr_count = sum(operation["kind"] == "pr" for operation in preview["operations"])

    if state["repository"] != preview["repository"] or audit["repository"] != preview["repository"] or not state["install_ready"] or state["plan_sha256"] != preview["plan_sha256"] or audit["plan_sha256"] != preview["plan_sha256"] or audit["remote_issues"] != issue_count or audit["remote_prs"] != pr_count or audit["ledger_rows"] != issue_count + pr_count or audit["closed_bot_prs_excluded"] or audit["action_policy"] != "stage":
        raise RuntimeError("Seed and audit do not establish a clean complete handoff")

    playbooks = {path.name: sha256(path) for path in sorted((seed.CODE_ROOT / "prompts").glob("*.md"))}
    program_commit = subprocess.run(["git", "-C", str(seed.CODE_ROOT), "rev-parse", "HEAD"], text=True, capture_output=True, check=True).stdout.strip()
    keys = [{"kind": operation["kind"], "number": state["items"][f"{operation['kind']}:{operation['id']}"]} for operation in preview["operations"]]

    if len(keys) != issue_count + pr_count or sum(key["kind"] == "issue" for key in keys) != issue_count or sum(key["kind"] == "pr" for key in keys) != pr_count or len({(key["kind"], key["number"]) for key in keys}) != len(keys):
        raise RuntimeError("Selected keys do not cover the audited issue and PR counts")

    gold_path = run_dir / "gold.json"

    if not gold_path.exists():
        raise RuntimeError(f"Run-specific withheld answer guide is missing: {gold_path}")

    public_scope = preview.get("public_read_scope")
    briefing = preview.get("briefing_batches")

    if args.run_id != seed.BASE_RUN_ID and (not public_scope or not briefing):
        raise RuntimeError("New runs require explicit public scope and briefing batches in the approved preview")

    handoff = {"repository": preview["repository"], "run_id": args.run_id, "seed_plan_sha256": preview["plan_sha256"], "gold_sha256": sha256(gold_path), "archive_sha256": preview["previous_archive_sha256"], "source_commit": preview["push_base_sha"], "program_commit": program_commit, "playbook_sha256": playbooks, "install_sha256": install_signature(preview["install_path"]) if preview.get("install_path") else None, "seeded_keys": keys, "counts": {"issue": issue_count, "pr": pr_count}, "public_read_scope": public_scope, "briefing_batches": briefing}
    (run_dir / "test-handoff.json").write_text(json.dumps(handoff, indent=2) + "\n")

    # The agent text names no path or file in this kit, so a copy can be handed over without leading to withheld material.
    scope = None if public_scope is None else ", ".join(f"{label} `{public_scope[field]}`" for field, label in (("repository", "repository"), ("corpus_id", "corpus"), ("inventory_snapshot", "inventory snapshot"), ("source_commit", "source commit")) if public_scope.get(field))
    lines = [
        f"# Agent handoff — {args.run_id}",
        "",
        "This describes the frozen starting state of an evaluation run. **It is not a task.** Each task arrives separately and says which playbook or request to follow and what to save; do that task and nothing wider.",
        "",
        f"- Private repository: `{preview['repository']}`, starting with {issue_count} open issues and {pr_count} open PRs. Its items are synthetic. Use your install's normal scripts to list and select them.",
        f"- Private base commit: `{preview['push_base_sha']}`. Program commit: `{program_commit}`.",
        *([f"- Public read scope: {scope}, read in `{public_scope['mode']}` mode. These are real historical items; keep them apart from the synthetic private ones."] if scope else []),
        "",
        "Work only from your own install, this handoff and the task text, and follow the install's `AGENTS.md`. Evaluation material outside your install is not an input: preparation files, expected answers, earlier runs' reports and other agents' notes. Do not open any of it, and say so in your notes if you did. No GitHub write is authorized unless a task says so explicitly.",
        "",
        "This handoff does not assert that any playbook pass has run.",
        "",
    ]
    (run_dir / "agent-handoff.md").write_text("\n".join(lines))
    print(run_dir / "test-handoff.json")
    print(run_dir / "agent-handoff.md")
    print("Give agents a copy of agent-handoff.md and their task text only, never a path into this kit.")

if __name__ == "__main__":
    main()
