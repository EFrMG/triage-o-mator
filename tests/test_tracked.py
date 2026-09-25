"""Tracked comment subscriptions use explicit reads and retain counts across partial checks."""

import json
import subprocess

from support import AcquisitionFixture, comment


class TrackedTests(AcquisitionFixture):
    def command(self, *args):
        result = subprocess.run([str(self.root / "bin/cache"), *args], cwd=self.root, env=self.env, capture_output=True, text=True, timeout=20)
        self.assertEqual(result.returncode, 0, result.stderr)
        return json.loads(result.stdout)

    def test_issue_tracking_checks_only_on_explicit_refresh_and_removes_without_deleting_evidence(self):
        (self.mock / "responses.json").write_text(json.dumps(self.responses))
        added = self.command("track-add", "--kind", "issue", "--number", "1")
        self.assertEqual(added["baseline_comments"], 1)
        self.assertEqual((self.root / "data/owner/repo/local/.gitignore").read_text(), "*\n")
        self.assertEqual(self.command("track-list")["rows"][0]["new_count"], 0)
        before = len(self.calls())
        self.command("track-list")
        self.assertEqual(len(self.calls()), before)

        self.responses["repos/owner/repo/issues/1"]["data"]["comments"] = 2
        self.responses["repos/owner/repo/issues/1/comments?per_page=100&page=1"] = dict(data=[comment(), comment(2)])
        (self.mock / "responses.json").write_text(json.dumps(self.responses))
        self.assertEqual(self.command("track-check")["checked"], 1)
        self.assertEqual(self.command("track-list")["rows"][0]["new_count"], 1)
        self.command("track-check")
        self.assertEqual(self.command("track-list")["rows"][0]["new_count"], 1)

        row = self.command("track-list")["rows"][0]
        self.assertNotIn("comment_ids", row)
        self.command("track-read", "--kind", "issue", "--number", "1", "--checked-at", row["checked_at"], "--new-count", "1")
        listing = self.command("track-list")
        self.assertEqual(listing["unread_total"], 0)
        self.assertEqual(listing["rows"][0]["new_count"], 0)
        stale = subprocess.run([str(self.root / "bin/cache"), "track-read", "--kind", "issue", "--number", "1", "--checked-at", row["checked_at"], "--new-count", "1"], cwd=self.root, env=self.env, capture_output=True, text=True, timeout=20)
        self.assertNotEqual(stale.returncode, 0)

        self.responses["repos/owner/repo/issues/1"]["data"]["comments"] = 3
        self.responses["repos/owner/repo/issues/1/comments?per_page=100&page=1"] = dict(data=[comment(), comment(2), comment(3)])
        (self.mock / "responses.json").write_text(json.dumps(self.responses))
        self.command("track-check")
        self.assertEqual(self.command("track-list")["unread_total"], 1)

        self.command("track-remove", "--kind", "issue", "--number", "1")
        self.assertEqual(self.command("track-list")["rows"], [])
        self.assertTrue((self.root / "data/owner/repo/cache/snapshots").exists())

    def test_incomplete_check_preserves_prior_count(self):
        (self.mock / "responses.json").write_text(json.dumps(self.responses))
        self.command("track-add", "--kind", "pr", "--number", "1")
        self.responses["repos/owner/repo/issues/1/comments?per_page=100&page=1"] = dict(status=503)
        (self.mock / "responses.json").write_text(json.dumps(self.responses))
        result = self.command("track-check")
        self.assertEqual(result["failed"], 1)
        row = self.command("track-list")["rows"][0]
        self.assertEqual(row["new_count"], 0)
        self.assertTrue(row["error"])

    def test_pr_inline_review_comment_counts_as_new(self):
        (self.mock / "responses.json").write_text(json.dumps(self.responses))
        self.command("track-add", "--kind", "pr", "--number", "1")
        existing = self.responses["repos/owner/repo/pulls/1/comments?per_page=100&page=1"]["data"][0]
        self.responses["repos/owner/repo/pulls/1"]["data"]["review_comments"] = 2
        self.responses["repos/owner/repo/pulls/1/comments?per_page=100&page=1"] = dict(data=[existing, dict(existing, id=21)])
        (self.mock / "responses.json").write_text(json.dumps(self.responses))
        self.command("track-check")
        self.assertEqual(self.command("track-list")["rows"][0]["new_count"], 1)
