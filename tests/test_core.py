"""The install, ledger, and duplicate workflows that a maintainer uses first."""

import json
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

from support import ROOT, Workspace, item


class InstallTests(unittest.TestCase):
    def test_install_dry_run_and_work_root(self):
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            checkout = base / "checkout"
            target = base / "target"
            target.mkdir()
            shutil.copytree(ROOT / "bin", checkout / "bin", ignore=shutil.ignore_patterns("triage-o-mator", "__pycache__"))
            for name in ("config", "themes", "prompts", "docs"):
                shutil.copytree(ROOT / name, checkout / name)
            for name in ("AGENTS.md", "README.md"):
                shutil.copyfile(ROOT / name, checkout / name)

            subprocess.run(["git", "init", "-q", str(target)], check=True)
            subprocess.run(["git", "-C", str(target), "remote", "add", "origin", "https://github.com/owner/repo.git"], check=True)
            env = dict(os.environ, XDG_CONFIG_HOME=str(base / "config-home"))
            command = [str(checkout / "bin/install-to"), str(target), "--yes"]
            preview = subprocess.run([*command, "--dry-run"], env=env, capture_output=True, text=True)
            self.assertEqual(preview.returncode, 0, preview.stderr)
            self.assertFalse((target / "triage-o-mator").exists())

            result = subprocess.run(command, env=env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            install = target / "triage-o-mator"
            self.assertTrue((install / "bin").is_symlink())
            self.assertEqual((install / "config/repo").read_text().strip(), "owner/repo")
            self.assertFalse((checkout / "data").exists())

            script = subprocess.run([str(install / "bin/group"), "create", "--title", "Review", "--description", "Related reports", "--by", "operator"], cwd=install, env=env, capture_output=True, text=True)
            self.assertEqual(script.returncode, 0, script.stderr)
            self.assertTrue(list((install / "data/owner/repo/groups").glob("*.json")))
            self.assertFalse((checkout / "data").exists())


class LedgerTests(Workspace):
    def setUp(self):
        super().setUp()
        rows = [item(1, "Suspend crashes on NVIDIA"), item(2, "NVIDIA suspend crash")]
        self.responses["repos/owner/repo/issues?state=open&per_page=100"] = dict(data=rows)

    def sync(self):
        self.run_cli("fetch")
        self.run_cli("sync")

    def test_fetch_sync_and_agent_proposal(self):
        self.sync()
        self.assertEqual(len(self.ledger()), 2)
        self.assertTrue(all(not row["reviewed"] for row in self.ledger().values()))

        ledger_path = self.root / "data/owner/repo/ledger.jsonl"
        before = ledger_path.read_bytes()
        args = ("--number", "1", "--kind", "issue", "--category", "bug", "--action", "label-only", "--reason", "Reproduced", "--by", "agent:triage")
        self.run_cli("apply", *args, "--dry-run")
        self.assertEqual(ledger_path.read_bytes(), before)
        self.run_cli("apply", *args)
        row = self.ledger()[("issue", 1)]
        self.assertEqual(row["triaged_by"], "agent:triage")
        self.assertFalse(row["reviewed"])

    def test_sync_preserves_proposals(self):
        self.sync()
        self.run_cli("apply", "--number", "1", "--kind", "issue", "--category", "bug", "--action", "label-only", "--by", "agent:triage")
        self.run_cli("fetch", "--full")
        self.run_cli("sync")
        self.assertEqual(self.ledger()[("issue", 1)]["category"], "bug")
        self.assertFalse(self.ledger()[("issue", 1)]["reviewed"])

    def test_negative_duplicate_verdict_excludes_pair(self):
        self.sync()
        before = self.json_cli("similar", "--pairs")["pairs"]
        self.assertEqual(len(before), 1)
        self.json_cli("not-duplicate", "--key", "issue:1", "--key", "issue:2", "--by", "maintainer", "--note", "Different cause")
        self.assertEqual(self.json_cli("similar", "--pairs")["pairs"], [])
        self.assertEqual(len(self.json_cli("similar", "--pairs", "--include-checked")["pairs"]), 1)
        self.assertFalse((self.mock / "calls.jsonl").read_text().count("POST"))


class TrackingTests(Workspace):
    def test_repeated_enrollment_keeps_the_comment_baseline(self):
        self.seed_pr()
        arguments = ("--expected-repo", "owner/repo", "track-add", "--kind", "pr", "--number", "1", "--request-budget", "20")
        first = self.json_cli("cache", *arguments)
        before_calls = self.calls()
        before_record = (self.root / "data/owner/repo/local/tracked-items.json").read_bytes()

        second = self.json_cli("cache", *arguments)
        self.assertFalse(first["already_tracking"])
        self.assertTrue(second["already_tracking"])
        self.assertEqual(second["requests"], 0)
        self.assertEqual(self.calls(), before_calls)
        self.assertEqual((self.root / "data/owner/repo/local/tracked-items.json").read_bytes(), before_record)
