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


class ItemContextTests(Workspace):
    def context(self, *args, ok=True):
        return self.json_cli("item-context", "--expected-repo", "owner/repo", *args, ok=ok)

    def test_offline_context_pages_and_revision_checked_text(self):
        absent = self.context("read", "--kind", "pr", "--number", "2")
        self.assertFalse(absent["ledger_present"])
        self.assertEqual(absent["group_count"], 0)
        self.assertEqual(absent["rows"][0]["fields"], {})

        row = dict(item(1, "Keep this change", "pr"), category="enhancement", action="keep-open", confidence="high",
                   reason="Maintainer wants the work", triaged_by="maintainer", triaged_at="2026-09-29T00:00:00Z",
                   agent_notes="Agent comparison", reviewed=True, reviewed_by="reviewer", reviewed_at="2026-09-29T01:00:00Z",
                   reviewer_notes="Wait for the dependency", last_synced_at="2026-09-29T02:00:00Z")
        ledger = self.root / "data/owner/repo/ledger.jsonl"
        ledger.write_text(json.dumps(row) + "\n")
        notes = "é" * 300
        group = self.json_cli("group", "create", "--title", "Shared decision", "--description", "Compare the fixes", "--by", "maintainer")
        self.run_cli("group", "add", group["id"], "--kind", "pr", "--number", "1", "--notes", notes, "--by", "maintainer")

        first = self.context("read", "--kind", "pr", "--number", "1", "--limit", "2")
        self.assertEqual(first["requests"], 0)
        self.assertTrue(first["ledger_present"])
        self.assertEqual(first["rows"][0]["fields"]["reviewed"], True)
        self.assertEqual(first["rows"][0]["fields"]["reviewer_notes"]["preview"], "Wait for the dependency")
        self.assertEqual(first["rows"][1]["revision"], 2)
        self.assertEqual(first["pagination"]["omitted_after"], 1)
        self.context("read", "--kind", "pr", "--number", "1", "--offset", "2", ok=False)

        second = self.context("read", "--kind", "pr", "--number", "1", "--offset", "2", "--limit", "2", "--checkpoint", first["checkpoint"])
        member = second["rows"][0]
        self.assertEqual(member["fields"]["updated_by"]["preview"], "maintainer")
        self.assertGreater(member["fields"]["notes"]["omitted_bytes"], 0)

        source = self.context("source", "--kind", "pr", "--number", "1", "--row", member["id"], "--field", "notes",
                              "--checkpoint", first["checkpoint"], "--max-bytes", "5")
        self.assertEqual(source["text"], "éé")
        self.assertEqual(source["continuation"]["byte_offset"], 4)

        row["last_synced_at"] = "2026-09-29T03:00:00Z"
        ledger.write_text(json.dumps(row) + "\n")
        self.assertEqual(self.context("read", "--kind", "pr", "--number", "1")["context_revision"], first["context_revision"])

        self.run_cli("group", "add", group["id"], "--kind", "pr", "--number", "1", "--notes", "New guidance", "--by", "maintainer")
        changed = self.context("read", "--kind", "pr", "--number", "1")
        self.assertNotEqual(changed["context_revision"], first["context_revision"])
        self.context("source", "--kind", "pr", "--number", "1", "--row", member["id"], "--field", "notes",
                     "--checkpoint", first["checkpoint"], ok=False)
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

    def test_closure_capture_produces_unknown_attribution_for_notifications(self):
        captured = self.json_cli("cache", "closure-capture", "--number", "1", "--by", "operator", "--request-budget", "100")
        entry = captured["history"]["entries"][0]
        self.assertEqual(captured["snapshot_id"], entry["observation"]["snapshot_id"])
        self.assertIsNone(entry["observation"]["operation_id"])
        self.assertEqual([source["component"] for source in entry["observation"]["sources"]], ["summary"])
        self.assertEqual(entry["claim"]["provenance"], "unknown")
        self.assertIsNone(entry["claim"]["rationale"])
        self.assertEqual(self.json_cli("cache", "action-list")["rows"][0]["history_checkpoint"], captured["history"]["checksum"])
        self.assertFalse((self.root / "data/owner/repo/cache/watches/pr-1.json").exists())
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())

        before = len(self.calls())
        self.run_cli("cache", "closure-capture", "--number", "1", "--by", "operator", ok=False)
        self.assertEqual(len(self.calls()), before)

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
