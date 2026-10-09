#!/usr/bin/env python3
"""Freeze a verified seed as an evaluator-only record."""

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


def clean_program_commit(code_root):
    """Bind the run to one committed program while allowing local untracked records."""
    status = subprocess.run(["git", "-C", str(code_root), "status", "--porcelain", "--untracked-files=no"], text=True, capture_output=True, check=True).stdout.strip()

    if status:
        raise RuntimeError("Program checkout has tracked changes; commit or revert them before freezing a run")

    return subprocess.run(["git", "-C", str(code_root), "rev-parse", "HEAD"], text=True, capture_output=True, check=True).stdout.strip()


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
        raise RuntimeError("Seed and audit do not establish a clean complete freeze")

    playbooks = {path.name: sha256(path) for path in sorted((seed.CODE_ROOT / "prompts").glob("*.md"))}
    program_commit = clean_program_commit(seed.CODE_ROOT)
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

    freeze = {"repository": preview["repository"], "run_id": args.run_id, "seed_plan_sha256": preview["plan_sha256"], "gold_sha256": sha256(gold_path), "archive_sha256": preview["previous_archive_sha256"], "source_commit": preview["push_base_sha"], "program_commit": program_commit, "playbook_sha256": playbooks, "install_sha256": install_signature(preview["install_path"]) if preview.get("install_path") else None, "seeded_keys": keys, "counts": {"issue": issue_count, "pr": pr_count}, "public_read_scope": public_scope, "briefing_batches": briefing}
    (run_dir / "freeze.json").write_text(json.dumps(freeze, indent=2) + "\n")

    print(run_dir / "freeze.json")
    print("Start each agent in its own install and give it only the frozen task text.")

if __name__ == "__main__":
    main()
