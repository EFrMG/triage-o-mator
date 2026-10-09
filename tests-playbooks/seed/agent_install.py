#!/usr/bin/env python3
"""Build one agent's isolated private or public install outside the kit, from a verified run freeze."""

import argparse
import datetime
import json
import os
from pathlib import Path
import re
import shutil
import subprocess

import seed
import freeze_run


AGENT_ROOT = Path(os.environ.get("TRIAGE_EVAL_AGENT_ROOT", "/tmp")).resolve()

# Ledger fields that describe the GitHub item. Everything else is a local decision by whoever used the source install, and is reset.
INVENTORY_FIELDS = ("number", "kind", "state", "title", "url", "author", "created_at", "updated_at", "labels", "comments_count", "first_seen_at", "last_synced_at", "state_reason")
DECISION_DEFAULTS = {"proposed_labels": [], "action": "", "confidence": "", "reason": "", "triaged_at": "", "triaged_by": "", "batch_id": "", "agent_notes": "", "review_request": None, "maintainer_notes": ""}
EXCLUDE_BLOCK = "\n# triage-o-mator, installed here but not part of this repository.\n/triage-o-mator/\n"


def git(path, *arguments):
    return subprocess.run(["git", "-C", str(path), *arguments], text=True, capture_output=True, check=True).stdout.strip()


def clone(source, target, origin, push_url=None):
    """A local copy of a pinned checkout, so base files are readable and nothing is shared with another agent."""
    head = git(source, "rev-parse", "HEAD")

    if git(source, "status", "--porcelain", "--untracked-files=no"):
        raise RuntimeError(f"Source checkout has tracked changes: {source}")

    subprocess.run(["git", "clone", "--quiet", "--no-hardlinks", str(source), str(target)], check=True)
    git(target, "remote", "set-url", "origin", origin)

    if push_url:
        git(target, "remote", "set-url", "--push", "origin", push_url)

    with (target / ".git" / "info" / "exclude").open("a") as handle:
        handle.write(EXCLUDE_BLOCK)

    if git(target, "rev-parse", "HEAD") != head:
        raise RuntimeError(f"Clone head differs from its source: {target}")

    return head


def scrubbed_ledger(source, target):
    """Copy inventory rows only. Returns how many rows carried a local decision."""
    rows = [json.loads(line) for line in source.read_text().splitlines() if line]
    cleared = 0
    lines = []

    for row in rows:
        clean = {field: row[field] for field in INVENTORY_FIELDS if field in row}
        clean.update({field: (list(value) if isinstance(value, list) else value) for field, value in DECISION_DEFAULTS.items()})
        cleared += any(row.get(field) != clean.get(field) for field in row)
        lines.append(json.dumps(clean, ensure_ascii=False))

    target.write_text("".join(line + "\n" for line in lines))

    return len(rows), cleared


def public_install(source_install, target_install, scope):
    """The frozen evidence cache and inventory, without any earlier agent's ledger decisions, assessments, batches, briefs or reports."""
    repository = scope["repository"]

    if (source_install / "config" / "repo").read_text().strip() != repository:
        raise RuntimeError("Public source install selects another repository")

    source_data = source_install / "data" / repository

    if not (source_data / "cache" / "corpora" / f"{scope['corpus_id']}.plan.json").is_file():
        raise RuntimeError("Public source install lacks the selected frozen corpus")

    target_install.mkdir()

    for entry in sorted(source_install.iterdir()):
        if entry.name in ("data", "reports"):
            continue

        if entry.is_symlink() or entry.is_file():
            shutil.copy2(entry, target_install / entry.name, follow_symlinks=False)

        else:
            shutil.copytree(entry, target_install / entry.name, symlinks=True)

    target_data = target_install / "data" / repository
    target_data.mkdir(parents=True)

    for name in ("cache", "raw"):
        shutil.copytree(source_data / name, target_data / name, symlinks=True)

    return scrubbed_ledger(source_data / "ledger.jsonl", target_data / "ledger.jsonl")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--run-id", required=True)
    parser.add_argument("--agent", required=True, help="short lowercase name for this agent, such as a or b")
    parser.add_argument("--lane", required=True, choices=("private", "public"))
    args = parser.parse_args()

    if not re.fullmatch(r"[a-z0-9]{1,12}", args.run_id) or not re.fullmatch(r"[a-z0-9]{1,12}", args.agent):
        parser.error("--run-id and --agent must be 1–12 lowercase letters or digits")

    run_dir = seed.RECORDS / "runs" / args.run_id
    freeze = json.loads((run_dir / "freeze.json").read_text())
    audit = json.loads((run_dir / "audit.json").read_text())
    preview = json.loads((run_dir / "preview.json").read_text())
    scope = freeze["public_read_scope"]

    if not scope:
        raise RuntimeError("This run's freeze has no public read scope, so no public install can be built")

    root = AGENT_ROOT / f"triage-eval-{args.run_id}" / args.agent

    if seed.CODE_ROOT in root.parents:
        raise RuntimeError("Agent installs must live outside the program checkout")

    if root.exists():
        raise RuntimeError(f"Agent directory already exists; choose another name or inspect it: {root}")

    seed_clone = Path(preview["clone_path"])
    seed_install = Path(preview["install_path"])

    if not freeze.get("install_sha256") or freeze_run.install_signature(seed_install) != freeze["install_sha256"]:
        raise RuntimeError("Seeded install changed since test-start; agents would not start from the audited state")

    if git(seed_clone, "rev-parse", "HEAD") != audit["local_checkout"] or git(seed.SOURCE_CLONE, "rev-parse", "HEAD") != freeze["source_commit"]:
        raise RuntimeError("Private or public source checkout moved since the audit")

    if freeze_run.clean_program_commit(seed.CODE_ROOT) != freeze["program_commit"]:
        raise RuntimeError("Program commit changed since test-start; record a new freeze before adding an agent")

    recorded = {path.name: freeze_run.sha256(path) for path in sorted((seed.CODE_ROOT / "prompts").glob("*.md"))}

    if recorded != freeze["playbook_sha256"]:
        raise RuntimeError("Playbooks changed since test-start; record a new freeze before adding an agent")

    root.mkdir(parents=True)

    if args.lane == "private":
        private_head = clone(seed_clone, root / "private", f"https://github.com/{freeze['repository']}.git")
        shutil.copytree(seed_install, root / "private" / "triage-o-mator", symlinks=True)
        lane_record = {"path": str(root / "private"), "repository": freeze["repository"], "head": private_head, "install_sha256": freeze_run.install_signature(root / "private" / "triage-o-mator")}

    else:
        public_head = clone(seed.SOURCE_CLONE, root / "public", f"https://github.com/{scope['repository']}.git", "no-push://public-read-only")
        rows, cleared = public_install(seed.SOURCE_CLONE / "triage-o-mator", root / "public" / "triage-o-mator", scope)
        lane_record = {"path": str(root / "public"), "repository": scope["repository"], "head": public_head, "corpus_id": scope["corpus_id"], "ledger_rows": rows, "ledger_rows_cleared": cleared}

    record = {"root": str(root), "lane": args.lane, "created_at": datetime.datetime.now(datetime.timezone.utc).isoformat(), "program_commit": git(seed.CODE_ROOT, "rev-parse", "HEAD"), args.lane: lane_record}
    index_path = run_dir / "agent-installs.json"
    index = json.loads(index_path.read_text()) if index_path.exists() else {}
    index[args.agent] = record
    temporary = index_path.with_name(".agent-installs.json.tmp")
    temporary.write_text(json.dumps(index, indent=2, sort_keys=True) + "\n")
    os.replace(temporary, index_path)

    print(f"Agent {args.agent}: {root}")
    print(f"  {args.lane} install: {root / args.lane / 'triage-o-mator'}")

    if args.lane == "public":
        print(f"  inventory:       {cleared} of {rows} ledger rows cleared of earlier decisions")

    print(f"  start in:        {root / args.lane / 'triage-o-mator'}")
    print("Give the agent its frozen task text. Do not give it any path into the kit.")


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, ValueError, KeyError, OSError, subprocess.CalledProcessError) as exc:
        raise SystemExit(f"agent install failed: {exc}")
