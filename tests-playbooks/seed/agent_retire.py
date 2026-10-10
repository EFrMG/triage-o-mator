#!/usr/bin/env python3
"""Retire one finished agent's disposable checkout while retaining its sealed install."""

import argparse
import datetime
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile


def sha256(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def signature(install):
    entries = []
    for path in sorted(install.rglob("*")):
        name = str(path.relative_to(install))
        if path.is_symlink():
            entries.append((name, "link", os.readlink(path)))
        elif path.is_file():
            entries.append((name, "file", sha256(path)))
        elif path.is_dir():
            entries.append((name, "directory"))
        else:
            raise ValueError(f"Unsupported install entry: {path}")

    return hashlib.sha256(json.dumps(entries, separators=(",", ":")).encode()).hexdigest()


def save(path, value):
    descriptor, temporary = tempfile.mkstemp(prefix=".retirement-", dir=path.parent)
    try:
        with os.fdopen(descriptor, "w") as out:
            out.write(json.dumps(value, indent=2) + "\n")
            out.flush()
            os.fsync(out.fileno())

        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def git(checkout, *args):
    return subprocess.check_output(["git", "--no-optional-locks", "-C", str(checkout), *args], text=True).strip()


def retire(run_dir, agent_root, agent):
    if run_dir.resolve() != run_dir:
        raise ValueError("Run records must not pass through a symlink")

    record = json.loads((run_dir / "agent-installs.json").read_text())[agent]
    lane = record["lane"]
    if lane not in ("private", "public"):
        raise ValueError("Agent must have one private or public lane")

    root = agent_root / f"triage-eval-{run_dir.name}" / agent
    checkout = root / lane
    install = checkout / "triage-o-mator"
    output = run_dir / "agents" / agent
    for path in (root.parent, root, checkout, install, output.parent, output):
        if path.is_symlink() or not path.is_dir():
            raise ValueError(f"Expected a real directory: {path}")

    if record["root"] != str(root) or record[lane]["path"] != str(checkout) or record["launch"]["working_directory"] != str(install):
        raise ValueError("Registered paths differ from the selected disposable agent root")

    with (output / ".retirement.lock").open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        seal_path = output / "seal.json"
        if seal_path.is_symlink():
            raise ValueError("Seal must not be a symlink")

        seal = json.loads(seal_path.read_text())
        if seal.get("agent") != agent or seal.get("assigned_working_directory") != str(install) or seal.get("task_complete_event") is not True or seal.get("final_output_found") is not True:
            raise ValueError("Retirement requires a sealed, completed attempt for this install")

        for name, key in (("transcript.jsonl", "transcript_sha256"), ("commands.jsonl", "command_log_sha256"), ("final.md", "output_sha256"), ("artifact-digests.json", "artifact_manifest_sha256")):
            path = output / name
            if path.is_symlink() or sha256(path) != seal[key]:
                raise ValueError(f"Sealed record changed: {name}")

        artifacts = json.loads((output / "artifact-digests.json").read_text())
        expected_files = set()
        for entry in artifacts:
            path = Path(entry["path"])
            if not path.is_absolute() or ".." in path.parts or install not in path.parents or path.is_symlink() or not path.is_file() or install.resolve() not in path.resolve().parents:
                raise ValueError("Artifact path escapes the retained install or is unavailable")
            if sha256(path) != entry["sha256"]:
                raise ValueError(f"Sealed install artifact changed: {path}")

            expected_files.add(path)

        actual_files = {path for path in install.rglob("*") if path.is_file() and not path.is_symlink()}
        if actual_files != expected_files or len(expected_files) != len(artifacts):
            raise ValueError("Install files differ from the sealed artifact manifest")

        before = signature(install)
        receipt_path = output / "retirement.json"
        if receipt_path.is_symlink():
            raise ValueError("Retirement receipt must not be a symlink")

        if receipt_path.exists():
            receipt = json.loads(receipt_path.read_text())
            if receipt.get("agent") != agent or receipt.get("checkout") != str(checkout) or receipt.get("install_retained") != str(install) or receipt.get("seal_sha256") != sha256(seal_path) or receipt.get("install_sha256") != before or receipt.get("status") not in ("prepared", "complete"):
                raise ValueError("Retirement receipt or retained install changed")
        else:
            if (checkout / ".git").is_symlink() or not (checkout / ".git").is_dir() or Path(git(checkout, "rev-parse", "--show-toplevel")).resolve() != checkout.resolve():
                raise ValueError("Expected the registered standalone Git checkout")

            head = git(checkout, "rev-parse", "HEAD")
            status = git(checkout, "status", "--porcelain", "--untracked-files=all", "--ignored=matching")
            changes = [line for line in status.splitlines() if line != "!! triage-o-mator/"]
            if head != record[lane]["head"] or changes:
                raise ValueError("Checkout HEAD changed or checkout has tracked/untracked changes")

            receipt = {"agent": agent, "checkout": str(checkout), "install_retained": str(install), "head_before_retirement": head, "seal_sha256": sha256(seal_path), "install_sha256": before, "removed_checkout_entries": sorted(path.name for path in checkout.iterdir() if path != install), "status": "prepared", "prepared_at": datetime.datetime.now(datetime.timezone.utc).isoformat()}
            save(receipt_path, receipt)

        names = receipt["removed_checkout_entries"]
        if not isinstance(names, list) or any(not isinstance(name, str) or name in ("", ".", "..", "triage-o-mator") or Path(name).name != name for name in names):
            raise ValueError("Invalid checkout entries in retirement receipt")

        remaining = {path.name for path in checkout.iterdir() if path != install}
        if not remaining.issubset(set(names)) or (receipt["status"] == "complete" and remaining):
            raise ValueError("Unexpected checkout entries appeared after retirement preparation")

        for name in sorted(remaining):
            path = checkout / name
            if path.is_symlink() or path.is_file():
                path.unlink()
            else:
                shutil.rmtree(path)

        if signature(install) != before:
            raise ValueError("Retained install changed during retirement")

        if receipt["status"] != "complete":
            receipt.update(status="complete", completed_at=datetime.datetime.now(datetime.timezone.utc).isoformat())
            save(receipt_path, receipt)

        return receipt


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--run-id", required=True)
    parser.add_argument("--agent", required=True)
    args = parser.parse_args()
    if any(not re.fullmatch(r"[a-z0-9]{1,12}", value) for value in (args.run_id, args.agent)):
        parser.error("run and agent IDs must be 1–12 lowercase letters or digits")

    code_root = Path(__file__).resolve().parents[2]
    records = Path(os.environ.get("TRIAGE_EVAL_RECORDS") or code_root / "DOCS/triage-playbook-evals").resolve()
    agent_root = Path(os.environ.get("TRIAGE_EVAL_AGENT_ROOT", "/tmp")).resolve()
    print(json.dumps(retire(records / "seed/runs" / args.run_id, agent_root, args.agent), indent=2))


if __name__ == "__main__":
    try:
        main()
    except (OSError, ValueError, KeyError, TypeError, subprocess.CalledProcessError) as error:
        raise SystemExit(f"agent retirement failed: {error}")
