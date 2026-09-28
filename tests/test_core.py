"""The install, ledger, and duplicate workflows that a maintainer uses first."""

import csv
import fcntl
import hashlib
import json
import os
import select
import shutil
import subprocess
import tempfile
import unittest
from datetime import datetime, timedelta, timezone
from pathlib import Path

from support import ROOT, Workspace, item, repository, summary


class InstallTests(unittest.TestCase):
    def test_solo_install_in_linked_worktree(self):
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            repository = base / "repository"
            worktree = base / "worktree"
            subprocess.run(["git", "init", "-q", str(repository)], check=True)
            subprocess.run(["git", "-C", str(repository), "config", "user.name", "Test"], check=True)
            subprocess.run(["git", "-C", str(repository), "config", "user.email", "test@example.com"], check=True)
            (repository / "README.md").write_text("fixture\n")
            subprocess.run(["git", "-C", str(repository), "add", "README.md"], check=True)
            subprocess.run(["git", "-C", str(repository), "-c", "commit.gpgsign=false", "commit", "-qm", "Initial"], check=True)
            subprocess.run(["git", "-C", str(repository), "worktree", "add", "-q", "--detach", str(worktree)], check=True)

            env = dict(os.environ, XDG_CONFIG_HOME=str(base / "config-home"))
            command = [str(ROOT / "bin/install-to"), str(worktree), "--repo", "owner/repo"]
            exclude = Path(subprocess.run(["git", "-C", str(worktree), "rev-parse", "--git-path", "info/exclude"], check=True, capture_output=True, text=True).stdout.strip())
            if not exclude.is_absolute():
                exclude = worktree / exclude

            before = exclude.read_bytes()

            preview = subprocess.run([*command, "--solo", "--dry-run"], env=env, capture_output=True, text=True)
            self.assertEqual(preview.returncode, 0, preview.stderr)
            self.assertEqual(exclude.read_bytes(), before)
            self.assertFalse((worktree / "triage-o-mator").exists())
            self.assertFalse((base / "config-home").exists())

            for _ in range(2):
                result = subprocess.run([*command, "--solo"], env=env, capture_output=True, text=True)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(exclude.read_text().splitlines().count("/triage-o-mator/"), 1)
                status = subprocess.run(["git", "-C", str(worktree), "status", "--porcelain"], check=True, capture_output=True, text=True)
                self.assertEqual(status.stdout, "")

            preview = subprocess.run([*command, "--adopt", "--dry-run"], env=env, capture_output=True, text=True)
            self.assertEqual(preview.returncode, 0, preview.stderr)
            self.assertEqual(exclude.read_text().splitlines().count("/triage-o-mator/"), 1)
            staged = subprocess.run(["git", "-C", str(worktree), "diff", "--cached", "--name-only"], check=True, capture_output=True, text=True)
            self.assertEqual(staged.stdout, "")

            result = subprocess.run([*command, "--adopt"], env=env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertNotIn("/triage-o-mator/", exclude.read_text().splitlines())
            staged = subprocess.run(["git", "-C", str(worktree), "diff", "--cached", "--name-only"], check=True, capture_output=True, text=True)
            self.assertIn("triage-o-mator/config/repo", staged.stdout.splitlines())

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

    def test_csv_review_revisions_and_approval(self):
        self.sync()
        ledger_path = self.root / "data/owner/repo/ledger.jsonl"
        self.run_cli("export-csv")
        csv_path = next((self.root / "data/owner/repo/exports").glob("ledger-*.csv"))

        with csv_path.open(newline="") as source:
            reader = csv.DictReader(source)
            fields, rows = reader.fieldnames, list(reader)

        rows[0].update(category="bug", action="label-only", reviewed="true", reviewed_by="human")
        with csv_path.open("w", newline="") as out:
            writer = csv.DictWriter(out, fieldnames=fields)
            writer.writeheader()
            writer.writerows(rows)

        self.run_cli("import-csv", str(csv_path), "--by", "operator")
        approved = self.ledger()[("issue", 1)]
        self.assertTrue(approved["reviewed"])
        self.assertEqual(approved["triaged_by"], "operator")

        self.run_cli("export-csv")
        with csv_path.open(newline="") as source:
            reader = csv.DictReader(source)
            fields, rows = reader.fieldnames, list(reader)
        ledger_rows = [json.loads(line) for line in ledger_path.read_text().splitlines()]
        ledger_rows[0]["last_synced_at"] = "2026-01-01T00:00:00Z"
        ledger_path.write_text("".join(json.dumps(row) + "\n" for row in ledger_rows))
        before = ledger_path.read_bytes()
        self.run_cli("import-csv", str(csv_path))
        self.assertEqual(ledger_path.read_bytes(), before)

        rows[0]["reason"] = "New rationale"
        with csv_path.open("w", newline="") as out:
            writer = csv.DictWriter(out, fieldnames=fields)
            writer.writeheader()
            writer.writerows(rows)
        self.run_cli("import-csv", str(csv_path), "--by", "operator")
        changed = self.ledger()[("issue", 1)]
        self.assertFalse(changed["reviewed"])
        self.assertEqual((changed["reviewed_by"], changed["reviewed_at"]), ("", ""))

        self.run_cli("export-csv")
        with csv_path.open(newline="") as source:
            reader = csv.DictReader(source)
            fields, rows = reader.fieldnames, list(reader)
        rows[0]["reviewer_notes"] = "Must not publish"
        rows[1]["reviewer_notes"] = "Keep this"
        with csv_path.open("w", newline="") as out:
            writer = csv.DictWriter(out, fieldnames=fields)
            writer.writeheader()
            writer.writerows(rows)
        self.run_cli("apply", "--number", "2", "--kind", "issue", "--category", "bug", "--action", "label-only", "--by", "agent:triage")
        before = ledger_path.read_bytes()
        self.run_cli("import-csv", str(csv_path), ok=False)
        self.assertEqual(ledger_path.read_bytes(), before)

    def test_ledger_writers_serialize_complete_transactions(self):
        self.responses["repos/owner/repo/issues?state=open&per_page=100"]["data"].append(item(3, "Another issue"))
        self.sync()
        self.run_cli("export-csv")
        calls_before = self.calls()
        data = self.root / "data/owner/repo"
        ledger_path = data / "ledger.jsonl"
        lock_path = data / "local/ledger.jsonl.lock"

        storage = self.root / "bin/_storage.py"
        source = storage.read_text()
        self.assertIn("        fcntl.flock(lock, fcntl.LOCK_EX)", source)
        source = source.replace(
            "        fcntl.flock(lock, fcntl.LOCK_EX)",
            "        if path.name == 'ledger.jsonl':\n            os.write(int(os.environ['LEDGER_TEST_NOTIFY_FD']), b'L')\n        fcntl.flock(lock, fcntl.LOCK_EX)",
        )
        storage.write_text(source)
        triage = self.root / "bin/_triage.py"
        source = triage.read_text()
        self.assertIn("def load_ledger():\n    return load_jsonl(LEDGER_PATH)", source)
        source = source.replace(
            "def load_ledger():\n    return load_jsonl(LEDGER_PATH)",
            "def load_ledger():\n    os.write(int(os.environ['LEDGER_TEST_NOTIFY_FD']), b'R')\n    return load_jsonl(LEDGER_PATH)",
        )
        triage.write_text(source)

        raw_path = data / "raw/issues_and_prs.jsonl"
        rows = [json.loads(line) for line in raw_path.read_text().splitlines()]
        rows[0]["title"] = "Fresh title"
        raw_path.write_text("".join(json.dumps(row) + "\n" for row in rows))
        meta_path = data / "raw/fetch_meta.json"
        meta = json.loads(meta_path.read_text())
        meta["raw_sha256"] = hashlib.sha256(raw_path.read_bytes()).hexdigest()
        meta_path.write_text(json.dumps(meta))
        exported = next((data / "exports").glob("ledger-*.csv"))
        csv_path = self.root / "notes.csv"
        with exported.open(newline="") as source, csv_path.open("w", newline="") as out:
            reader = csv.DictReader(source)
            writer = csv.DictWriter(out, fieldnames=reader.fieldnames)
            writer.writeheader()
            row = next(row for row in reader if row["kind"] == "issue" and row["number"] == "3")
            row["reviewer_notes"] = "Separate note"
            writer.writerow(row)

        read_fd, write_fd = os.pipe()
        commands = [
            ["apply", "--number", "1", "--kind", "issue", "--category", "bug", "--action", "label-only"],
            ["apply", "--number", "2", "--kind", "issue", "--category", "bug", "--action", "label-only"],
            ["sync"],
            ["import-csv", str(csv_path)],
        ]
        processes = []
        try:
            with lock_path.open("a") as lock:
                fcntl.flock(lock, fcntl.LOCK_EX)
                for args in commands:
                    env = dict(self.env, TRIAGE_ROOT=str(self.root), LEDGER_TEST_NOTIFY_FD=str(write_fd))
                    processes.append(subprocess.Popen([str(self.root / "bin" / args[0]), *args[1:]], cwd=self.root, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, pass_fds=(write_fd,)))

                reached = bytearray()
                while len(reached) < len(commands):
                    ready, _, _ = select.select([read_fd], [], [], 10)
                    self.assertTrue(ready, "ledger writer did not reach the lock")
                    reached.extend(os.read(read_fd, len(commands) - len(reached)))

                self.assertEqual(reached, b"L" * len(commands))
                ready, _, _ = select.select([read_fd], [], [], 0)
                self.assertFalse(ready, "a ledger read occurred while the lock was held")
                fcntl.flock(lock, fcntl.LOCK_UN)

            for process in processes:
                stdout, stderr = process.communicate(timeout=10)
                self.assertEqual(process.returncode, 0, stdout + stderr)
        finally:
            for process in processes:
                if process.poll() is None:
                    process.kill()
                process.communicate()
            os.close(read_fd)
            os.close(write_fd)

        ledger = self.ledger()
        self.assertEqual(ledger[("issue", 1)]["category"], "bug")
        self.assertEqual(ledger[("issue", 2)]["category"], "bug")
        self.assertEqual(ledger[("issue", 3)]["reviewer_notes"], "Separate note")
        self.assertEqual(ledger[("issue", 1)]["title"], "Fresh title")
        self.assertEqual(self.calls(), calls_before)

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
    def test_budgeted_checks_rotate_starting_subscription(self):
        self.responses["repos/owner/repo"] = dict(data=repository())
        for number in (1, 2):
            self.responses[f"repos/owner/repo/issues/{number}"] = dict(data=summary("issue", number))
            self.responses[f"repos/owner/repo/issues/{number}/comments?per_page=100&page=1"] = dict(data=[])

        arguments = ("--expected-repo", "owner/repo")
        for number in (1, 2):
            self.json_cli("cache", *arguments, "track-add", "--kind", "issue", "--number", str(number), "--request-budget", "10")
            self.responses[f"repos/owner/repo/issues/{number}"]["data"]["comments"] = 1
            self.responses[f"repos/owner/repo/issues/{number}/comments?per_page=100&page=1"]["data"] = [dict(id=number, body="new", user=dict(login="author"), created_at="2026-09-23T00:00:00Z", updated_at="2026-09-23T00:00:00Z")]

        def check(budget):
            before = len(self.calls())
            result = self.json_cli("cache", *arguments, "track-check", "--request-budget", str(budget))
            calls = self.calls()[before:]
            summaries = [call[-1] for call in calls if call[-1].startswith("repos/owner/repo/issues/") and "?" not in call[-1]]
            return result, summaries

        first, summaries = check(4)
        self.assertEqual(summaries[0], "repos/owner/repo/issues/1")
        self.assertEqual(first["checked"], 1)

        second, summaries = check(5)
        self.assertEqual(summaries[0], "repos/owner/repo/issues/2")
        self.assertEqual(second["checked"], 1)
        self.assertEqual(second["unread_total"], 2)

        third, summaries = check(3)
        self.assertEqual(summaries[0], "repos/owner/repo/issues/1")
        self.assertEqual(third["checked"], 0)

        fourth, summaries = check(4)
        self.assertEqual(summaries[0], "repos/owner/repo/issues/2")
        self.assertEqual(fourth["unread_total"], 2)
        self.assertEqual([row["new_count"] for row in self.json_cli("cache", *arguments, "track-list")["rows"]], [1, 1])

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
