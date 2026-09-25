"""CLI integration tests in isolated checkouts; never mutate real triage data."""

import json
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


class GroupTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        shutil.copytree(
            ROOT / "bin",
            self.root / "bin",
            ignore=shutil.ignore_patterns("triage-o-mator", "__pycache__"),
        )
        shutil.copytree(ROOT / "config", self.root / "config")
        # What bin/install-to writes to mark an install; the scripts refuse to run anywhere else.
        (self.root / ".triage-install.json").write_text('{"tool": "%s"}\n' % ROOT)
        (self.root / "config/repo").write_text("owner/repo\n")
        (self.root / "data/owner/repo").mkdir(parents=True)
        self.ledger = self.root / "data/owner/repo/ledger.jsonl"
        self.records = [
            dict(
                kind=kind,
                number=n,
                title=f"Item {n}",
                state="open",
                category="bug" if n == 1 else "",
                action="label-only" if n == 1 else "",
                confidence="high",
                reason="Evidence",
                reviewed=False,
                created_at="2025-01-01",
                triaged_by="agent",
                labels=[],
                url=f"https://github.com/owner/repo/issues/{n}",
            )
            for kind, n in [("issue", 1), ("pr", 2), ("issue", 3)]
        ]
        self.ledger.write_text("".join(json.dumps(r) + "\n" for r in self.records))
        self.original = self.ledger.read_bytes()
        self.env = os.environ.copy()
        mock = self.root / "mock"
        mock.mkdir()
        gh = mock / "gh"
        gh.write_text(
            '#!/usr/bin/env python3\nimport json,sys\nif sys.argv[2]=="diff": print("+change")\nelse: print(json.dumps({"body":"Full body", "comments":[{"body":f"Comment {i}"} for i in range(9)]}))\n'
        )
        gh.chmod(0o755)
        self.env["PATH"] = str(mock) + os.pathsep + self.env["PATH"]

    def run_cli(self, *args, ok=True, command="group"):
        result = subprocess.run(
            [str(self.root / "bin" / command), *args],
            cwd=self.root,
            env=self.env,
            text=True,
            capture_output=True,
        )
        if ok:
            self.assertEqual(result.returncode, 0, result.stderr)
        else:
            self.assertNotEqual(result.returncode, 0)

        return result

    def create(self):
        return json.loads(
            self.run_cli(
                "create",
                "--title",
                "Related bugs",
                "--description",
                "Review together",
                "--by",
                "Alice",
            ).stdout
        )

    def test_handoff_and_revisions(self):
        g = self.create()
        gid = g["id"]
        self.run_cli(
            "add",
            gid,
            "--kind",
            "issue",
            "--number",
            "1",
            "--notes",
            "Root cause",
            "--revision",
            "1",
            "--by",
            "Bob",
        )
        rejected = self.run_cli(
            "update",
            gid,
            "--title",
            "Stale",
            "--revision",
            "1",
            "--by",
            "Alice",
            ok=False,
        )
        self.assertIn("changed since", rejected.stderr)
        self.run_cli(
            "add",
            gid,
            "--kind",
            "pr",
            "--number",
            "2",
            "--notes",
            "Fixes issue #1",
            "--by",
            "Bob",
        )
        self.run_cli(
            "update",
            gid,
            "--status",
            "ready",
            "--assignee",
            "Maintainer",
            "--by",
            "Alice",
        )
        exported = self.run_cli("export", gid, "--format", "json", "--diff")
        data = json.loads(exported.stdout)
        self.assertEqual(
            exported.stderr.splitlines(),
            ["fetching 1/2: issue #1", "fetching 2/2: pr #2"],
        )
        self.assertEqual(data["group"]["status"], "ready")
        self.assertEqual(data["group"]["created_by"], "Alice")
        self.assertEqual(data["group"]["members"][0]["added_by"], "Bob")
        self.assertEqual(len(data["items"][0]["comment_bodies"]), 9)
        self.assertIn("+change", data["items"][1]["diff_text"])
        self.assertFalse(data["items"][0]["reviewed"])
        md = self.run_cli("export", gid).stdout
        for text in (
            "Cross-references",
            "Root cause",
            "Fixes issue #1",
            "Evidence",
            "reviewed: False",
        ):
            self.assertIn(text, md)

        self.assertEqual(self.original, self.ledger.read_bytes())

    def test_legacy_comparison_data_stays_in_group_file_but_not_review_packet(self):
        group = self.create()
        path = self.root / "data/owner/repo/groups" / f"{group['id']}.json"
        group["comparison"] = {"retired": "historical claim"}
        group["comparison_findings"] = [{"retired": "finding"}]
        path.write_text(json.dumps(group))

        self.run_cli("update", group["id"], "--description", "Ready for a maintainer", "--by", "Alice")
        stored = json.loads(path.read_text())
        packet = json.loads(self.run_cli("export", group["id"], "--format", "json").stdout)

        self.assertIn("comparison", stored)
        self.assertIn("comparison_findings", stored)
        self.assertNotIn("comparison", packet["group"])
        self.assertNotIn("comparison_findings", packet["group"])
        self.assertEqual(packet["group"]["description"], "Ready for a maintainer")

    def test_validation_membership_and_repo_isolation(self):
        gid = self.create()["id"]
        self.run_cli("update", gid, "--title", " ", "--by", "Alice", ok=False)
        self.run_cli("show", "../ledger", ok=False)
        self.run_cli(
            "add", gid, "--kind", "issue", "--number", "999", "--by", "Alice", ok=False
        )
        for note in ("first", "second"):
            self.run_cli(
                "add",
                gid,
                "--kind",
                "issue",
                "--number",
                "1",
                "--notes",
                note,
                "--by",
                "Alice",
            )

        g = json.loads(self.run_cli("show", gid).stdout)
        self.assertEqual(len(g["members"]), 1)
        self.assertEqual(g["members"][0]["notes"], "second")
        self.run_cli("remove", gid, "--kind", "issue", "--number", "1", "--by", "Alice")
        self.assertEqual(json.loads(self.run_cli("show", gid).stdout)["members"], [])
        (self.root / "config/repo").write_text("other/repo\n")
        self.assertEqual(json.loads(self.run_cli("list").stdout), [])
        self.run_cli("show", gid, ok=False)
        self.assertEqual(self.original, self.ledger.read_bytes())

    def test_delete_takes_the_group_and_nothing_else(self):
        gid = self.create()["id"]
        self.run_cli("add", gid, "--kind", "issue", "--number", "1", "--notes", "Root cause", "--by", "Alice")

        stale = self.run_cli("delete", gid, "--revision", "1", ok=False)
        self.assertIn("changed since", stale.stderr)
        self.assertIn(gid, json.loads(self.run_cli("list").stdout)[0]["id"], "a stale delete must not remove the group")

        deleted = json.loads(self.run_cli("delete", gid, "--revision", "2").stdout)["deleted"]
        self.assertEqual(deleted["members"][0]["notes"], "Root cause", "the deleted group is the run's only record of it")
        self.assertEqual(json.loads(self.run_cli("list").stdout), [])
        self.assertFalse((self.root / f"data/owner/repo/groups/{gid}.json").exists())
        self.run_cli("show", gid, ok=False)
        self.run_cli("delete", gid, ok=False)
        self.assertEqual(self.original, self.ledger.read_bytes())

    def test_delete_refuses_another_repos_group_and_paths(self):
        gid = self.create()["id"]
        self.run_cli("delete", "../ledger", ok=False)
        (self.root / "config/repo").write_text("other/repo\n")
        self.run_cli("delete", gid, ok=False)
        (self.root / "config/repo").write_text("owner/repo\n")

        self.assertEqual(len(json.loads(self.run_cli("list").stdout)), 1, "the group survived a delete aimed from another repository")

    def test_concurrent_writers_keep_both_members(self):
        gid = self.create()["id"]
        procs = [
            subprocess.Popen(
                [
                    str(self.root / "bin/group"),
                    "add",
                    gid,
                    "--kind",
                    kind,
                    "--number",
                    str(n),
                    "--by",
                    "Alice",
                ],
                stdout=subprocess.PIPE,
                stderr=subprocess.PIPE,
            )
            for kind, n in [("issue", 1), ("pr", 2)]
        ]
        for proc in procs:
            _, err = proc.communicate()
            self.assertEqual(proc.returncode, 0, err)

        self.assertEqual(
            len(json.loads(self.run_cli("show", gid).stdout)["members"]), 2
        )

    def test_group_batch_and_report(self):
        gid = self.create()["id"]
        for kind, number in [("issue", "1"), ("pr", "2")]:
            self.run_cli(
                "add",
                gid,
                "--kind",
                kind,
                "--number",
                number,
                "--notes",
                "Read together",
                "--by",
                "Alice",
            )

        self.run_cli("25", "--group", gid, command="batch")
        path = next((self.root / "data/owner/repo/batches").glob("*.items.jsonl"))
        rows = [json.loads(line) for line in path.read_text().splitlines()]
        self.assertEqual([r["number"] for r in rows], [2])
        self.assertEqual(rows[0]["group"]["title"], "Related bugs")
        self.assertEqual(rows[0]["group_member_notes"], "Read together")
        self.assertIn("Related bugs", self.run_cli("--stdout", command="report").stdout)
        self.assertEqual(self.original, self.ledger.read_bytes())

    def test_a_ready_group_is_a_decision_waiting_for_its_case(self):
        gid = self.create()["id"]
        self.run_cli("add", gid, "--kind", "issue", "--number", "1", "--notes", "The original", "--by", "Alice")

        nxt = self.run_cli("--json", command="next").stdout
        self.assertNotIn("polish-report.md", nxt, "a draft group is the contributor's to finish first")

        self.run_cli("update", gid, "--status", "ready", "--by", "Alice")
        steps = json.loads(self.run_cli("--json", command="next").stdout)["suggestions"]
        polish = [s for s in steps if "polish-report.md" in s["do"]]
        self.assertEqual(len(polish), 1, steps)
        self.assertEqual(polish[0]["who"], "agent")
        self.assertIn("Related bugs", polish[0]["what"])


if __name__ == "__main__":
    unittest.main()
