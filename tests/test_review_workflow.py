"""Local review aids preserve the distinction between context and approval."""

import json

from support import Workspace, item


class GroupTests(Workspace):
    def test_ready_group_never_changes_member_decision(self):
        row = dict(item(1, "Related issue"), category="bug", action="label-only", confidence="medium", reason="Proposal", reviewed=False)
        ledger = self.root / "data/owner/repo/ledger.jsonl"
        ledger.write_text(json.dumps(row) + "\n")
        original = ledger.read_bytes()

        group = self.json_cli("group", "create", "--title", "Related reports", "--description", "Review together", "--by", "operator")
        self.run_cli("group", "add", group["id"], "--kind", "issue", "--number", "1", "--by", "operator")
        self.run_cli("group", "update", group["id"], "--status", "ready", "--by", "operator")
        self.assertEqual(ledger.read_bytes(), original)
        self.assertFalse(self.ledger()[("issue", 1)]["reviewed"])
        self.assertEqual(self.calls(), [])


class WatchTests(Workspace):
    def setUp(self):
        super().setUp()
        self.seed_pr("closed")

    def acquire(self):
        return self.json_cli("cache", "fetch", "--kind", "pr", "--number", "1", "--profile", "closure-watch", "--mode", "refresh", "--request-budget", "100")["snapshot_id"]

    def test_watch_enrollment_and_read_are_offline(self):
        snapshot = self.acquire()
        enrolled = self.json_cli("cache", "watch-enroll", "--number", "1", "--snapshot", snapshot, "--by", "operator", "--closure-event", "99")
        before = len(self.calls())
        shown = self.json_cli("cache", "watch-show", "--number", "1")
        self.assertEqual(shown["watch"], enrolled["watch"])
        self.assertEqual(len(self.calls()), before)
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())

    def test_imported_closure_does_not_enroll_or_approve(self):
        snapshot = self.acquire()
        claim = dict(external=dict(namespace="runner", record_id="run-1"), actor=None, run_id=None, rationale="Retained explanation", survivor=None, provenance="unknown", supports=[])
        path = self.root / "claim.json"
        path.write_text(json.dumps(claim))
        before = len(self.calls())
        imported = self.json_cli("cache", "closure-import", "--number", "1", "--snapshot", snapshot, "--by", "operator", "--claim", str(path), "--closure-event", "99")
        listing = self.json_cli("cache", "action-list")
        self.assertEqual(listing["rows"][0]["history_checkpoint"], imported["history"]["checksum"])
        self.assertEqual(len(self.calls()), before)
        self.assertFalse((self.root / "data/owner/repo/cache/watches/pr-1.json").exists())
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())

    def test_watch_poll_records_activity_without_approval(self):
        snapshot = self.acquire()
        self.json_cli("cache", "watch-enroll", "--number", "1", "--snapshot", snapshot, "--by", "operator", "--closure-event", "99")
        self.responses["repos/owner/repo/pulls/1"]["data"].update(updated_at="2026-09-23T00:00:00Z", comments=1)
        self.responses["repos/owner/repo/issues/1/comments?per_page=100&page=1"] = dict(data=[dict(id=1, node_id="C_1", html_url="https://github.com/owner/repo/issues/1#issuecomment-1", body="Please reconsider", user=dict(login="author"), created_at="2026-09-23T00:00:00Z", updated_at="2026-09-23T00:00:00Z")])
        self.responses["repos/owner/repo/issues/1/timeline?per_page=100&page=1"]["data"].append(dict(id=1, event="commented", created_at="2026-09-23T00:00:00Z"))
        result = self.json_cli("cache", "watch-poll", "--number", "1", "--restart")
        self.assertEqual(result["observation_count"], 2)
        self.assertEqual(result["poll"]["status"], "complete")
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())
