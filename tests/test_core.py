"""The install, ledger, and duplicate workflows that a maintainer uses first."""

import json
import os
import shutil
import subprocess
import tempfile
import unittest
from datetime import datetime, timedelta, timezone
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
            command = [str(checkout / "bin/install-to"), str(target)]
            preview = subprocess.run([*command, "--dry-run"], env=env, capture_output=True, text=True)
            self.assertEqual(preview.returncode, 0, preview.stderr)
            self.assertFalse((target / "triage-o-mator").exists())
            self.assertFalse((target / "AGENTS.md").exists())

            result = subprocess.run(command, env=env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            install = target / "triage-o-mator"
            self.assertTrue((install / "bin").is_symlink())
            self.assertTrue((install / "AGENTS.md").is_symlink())
            self.assertIn("<!-- triage-o-mator:begin -->", (target / "AGENTS.md").read_text())
            self.assertEqual((install / "config/repo").read_text().strip(), "owner/repo")
            self.assertFalse((checkout / "data").exists())

            script = subprocess.run([str(install / "bin/group"), "create", "--title", "Review", "--description", "Related reports", "--by", "operator"], cwd=install, env=env, capture_output=True, text=True)
            self.assertEqual(script.returncode, 0, script.stderr)
            self.assertTrue(list((install / "data/owner/repo/groups").glob("*.json")))
            self.assertFalse((checkout / "data").exists())

            (target / "AGENTS.md").write_text("# Own instructions\n")
            result = subprocess.run(command, env=env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((target / "AGENTS.md").read_text(), "# Own instructions\n")
            result = subprocess.run([*command, "--yes"], env=env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("<!-- triage-o-mator:begin -->", (target / "AGENTS.md").read_text())

            claude_target = base / "claude-target"
            claude_target.mkdir()
            subprocess.run(["git", "init", "-q", str(claude_target)], check=True)
            subprocess.run(["git", "-C", str(claude_target), "remote", "add", "origin", "https://github.com/owner/repo.git"], check=True)
            (claude_target / "CLAUDE.md").write_text("# Existing Claude instructions\n")
            result = subprocess.run([str(checkout / "bin/install-to"), str(claude_target)], env=env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("<!-- triage-o-mator:begin -->", (claude_target / "AGENTS.md").read_text())
            self.assertEqual((claude_target / "CLAUDE.md").read_text(), "# Existing Claude instructions\n")


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

    def test_direct_item_read_overrides_lagging_issue_list_after_close(self):
        self.sync()
        meta = json.loads((self.root / "data/owner/repo/raw/fetch_meta.json").read_text())
        since = (datetime.strptime(meta["synced_through"], "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc) - timedelta(minutes=5)).strftime("%Y-%m-%dT%H:%M:%SZ")
        self.responses[f"repos/owner/repo/issues?state=all&since={since}&per_page=100"] = dict(data=[item(1, "Suspend crashes on NVIDIA")])
        self.responses["repos/owner/repo/issues/1"] = dict(data=dict(number=1, html_url="https://github.com/owner/repo/issues/1", state="closed", title="Suspend crashes on NVIDIA", user=dict(login="author"), created_at="2025-01-01T00:00:00Z", updated_at="2025-01-02T00:00:00Z", labels=[], comments=1))

        self.run_cli("fetch", "--include-item", "issue:1")
        self.run_cli("sync")
        self.assertEqual(self.ledger()[("issue", 1)]["state"], "closed")
        self.assertEqual(self.ledger()[("issue", 2)]["state"], "open")

    def test_direct_item_read_pins_enterprise_host_for_list_and_item(self):
        host = "ghe.example"
        row = item(1, "Enterprise issue")
        row["url"] = f"https://{host}/owner/repo/issues/1"
        self.responses["repos/owner/repo/issues?state=open&per_page=100"] = dict(data=[row])
        direct = dict(number=1, html_url="https://github.com/owner/repo/issues/1", state="open", title=row["title"], user=dict(login="author"), created_at=row["created_at"], updated_at="2026-09-27T00:00:00Z", labels=[], comments=0)
        self.responses["repos/owner/repo/issues/1"] = dict(data=direct)

        self.run_cli("fetch", "--host", host, "--include-item", "issue:1", ok=False)
        self.assertFalse((self.root / "data/owner/repo/raw/issues_and_prs.jsonl").exists())

        direct["html_url"] = row["url"]
        self.run_cli("fetch", "--host", host, "--include-item", "issue:1")
        self.run_cli("sync")
        self.assertEqual(self.ledger()[("issue", 1)]["url"], row["url"])
        self.assertEqual(json.loads((self.root / "data/owner/repo/raw/fetch_meta.json").read_text())["host"], host)
        self.assertTrue(all("--hostname" in call and call[call.index("--hostname") + 1] == host for call in self.calls()))

        calls = self.calls()
        self.run_cli("fetch", "--host", "github.com", ok=False)
        self.assertEqual(self.calls(), calls)
        self.assertEqual(self.ledger()[("issue", 1)]["url"], row["url"])

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
