"""Closed watches use disposable installs and a strict read-only fake GitHub."""

import json


from support import WatchFixture, comment, repo, summary


class WatchTests(WatchFixture):
    def test_initial_activity_absent_ledger_and_offline_reader(self):
        result = self.enroll("--closure-comment", "1", "--closure-event", "99", "--survivor", "2")
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())
        self.assertTrue(result["baseline_complete"])
        self.assertIsNone(result["watch"]["provenance"])
        self.assertEqual(result["watch"]["survivor"], 2)
        observation = result["observations"][0]
        self.assertEqual(observation["acknowledgment"], "not-tracked")
        self.assertTrue(any(row["relation"] == "at-or-after-closure" and row["source"]["id"] == 1 for row in observation["activity"] if "id" in row["source"]))
        self.assertTrue(any(row["supplied_closure_reference"] for row in observation["activity"]))
        calls = self.calls()
        self.assertEqual(self.watch("watch-show"), result)
        self.assertEqual(calls, self.calls())
        self.assertTrue(all(call[call.index("--method") + 1] == "GET" for call in calls))

    def test_capture_retains_history_reopen_and_last_successful_check(self):
        first = self.enroll()
        self.responses["repos/owner/repo/pulls/1"] = dict(data=summary(state="open", closed_at=None, updated_at="2026-09-23T00:00:00Z", comments=2))
        self.responses["repos/owner/repo/issues/1/comments?per_page=100&page=1"] = dict(data=[comment(), dict(comment(2), body="Please reconsider", updated_at="2026-09-23T00:00:00Z")])
        self.responses[self.timeline]["data"].append(dict(id=100, event="reopened", created_at="2026-09-23T00:00:00Z"))
        result = self.watch("watch-capture", "--request-budget", "5")
        self.assertLessEqual(result["requests"], 5)
        self.assertEqual(len(result["observations"]), 2)
        self.assertEqual(result["observations"][0], first["observations"][0])
        self.assertEqual(result["observations"][-1]["state"], "open")
        # Reopening retains the original enrollment boundary instead of losing post-closure activity.
        self.assertEqual(result["observations"][-1]["closure_boundary"], "2026-09-21T00:00:00Z")
        self.assertTrue(result["observations"][-1]["complete"])
        self.assertEqual(result["last_successful_check"], result["observations"][-1]["observed_at"])

    def test_partial_enrollment_never_claims_baseline_and_missing_reference(self):
        result = self.enroll("--closure-comment", "777", budget="3")
        self.assertFalse(result["baseline_complete"])
        self.assertIsNone(result["last_successful_check"])
        self.assertTrue(any("not observed" in " ".join(gap["problems"]) for gap in result["observations"][0]["gaps"]))
        complete = self.watch("watch-capture", "--request-budget", "5")
        self.assertFalse(complete["baseline_complete"])
        self.assertFalse(complete["observations"][-1]["complete"])

    def test_budget_failure_preserves_success_and_publishes_partial(self):
        first = self.enroll()
        result = self.watch("watch-capture", "--request-budget", "2")
        self.assertEqual(result["requests"], 2)
        self.assertFalse(result["observations"][-1]["complete"])
        self.assertEqual(result["last_successful_check"], first["last_successful_check"])
        self.assertEqual(len(result["observations"]), 2)

    def test_refuse_identity_conflict_and_keep_record(self):
        first = self.enroll()
        self.responses["repos/owner/repo"] = dict(data=dict(repo(), id=999))
        self.assertIn("mismatch", self.watch("watch-capture", ok=False))
        self.assertEqual(self.watch("watch-show")["watch"], first["watch"])

    def test_open_enrollment_duplicate_and_future_schema_refused(self):
        first = self.enroll()
        self.assertIn("already watched", self.watch("watch-enroll", "--snapshot", first["watch"]["observations"][0], "--by", "operator", ok=False))
        path = self.root / "data/owner/repo/cache/watches/pr-1.json"
        value = json.loads(path.read_text())
        value["schema_version"] = 99
        path.write_text(json.dumps(value))
        calls = self.calls()
        self.watch("watch-capture", ok=False)
        self.assertEqual(calls, self.calls())

    def test_unrecognized_timeline_identity_and_pagination_gap(self):
        self.responses[self.timeline] = dict(data=[dict(event="unknown", created_at="2026-09-21T00:00:00Z")])
        result = self.enroll()
        self.assertFalse(result["baseline_complete"])
        self.assertIn("identity", json.dumps(result["observations"][0]["gaps"]))

    def test_timeline_pagination_and_zero_network_offline_integrity_failure(self):
        self.responses[self.timeline]["headers"] = {"link": '<https://api.github.com/next>; rel="next"'}
        self.responses["repos/owner/repo/issues/1/timeline?per_page=100&page=2"] = dict(data=[dict(id=100, event="reopened", created_at="2026-09-23T00:00:00Z")])
        result = self.enroll()
        self.assertTrue(result["baseline_complete"])
        snapshot = result["watch"]["observations"][0]
        manifest = json.loads((self.root / f"data/owner/repo/cache/snapshots/{snapshot}.json").read_text())
        ref = manifest["items"][0]["components"]["timeline"]["object"]
        objects = self.root / "data/owner/repo/cache/objects"
        target = next(path for path in objects.iterdir() if ref["sha256"] in path.name)
        target.write_text("corrupted")
        calls = self.calls()
        self.watch("watch-show", ok=False)
        self.watch("watch-capture", ok=False)
        self.assertEqual(calls, self.calls())
