"""Bounded attention readers use disposable watches; every read must stay offline."""

import json
import subprocess
import sys
import time


from support import WatchFixture, comment


class AttentionTests(WatchFixture):
    def attention(self, command="list", *args, ok=True):
        result = subprocess.run([str(self.root / "bin/cache"), "attention-" + command, *args],
                                cwd=self.root, env=self.env, capture_output=True, text=True, timeout=20)
        if not ok:
            self.assertNotEqual(result.returncode, 0, result.stdout)
            return result.stderr

        self.assertEqual(result.returncode, 0, result.stderr)
        return json.loads(result.stdout)

    def detail(self, section, token, *args, ok=True):
        return self.attention("read", "--number", "1", "--section", section, "--checkpoint", token, *args, ok=ok)

    def source(self, section, token, entry, *args, ok=True):
        return self.attention("source", "--number", "1", "--section", section, "--checkpoint", token, "--entry", entry, *args, ok=ok)

    def token(self):
        return self.attention()["rows"][0]["watch_checkpoint"]

    def test_empty_cache_is_offline_and_not_initialized(self):
        result = self.attention()
        self.assertEqual(result["pagination"]["total"], 0)
        self.assertEqual(self.calls(), [])
        self.assertFalse((self.root / "data/owner/repo/cache").exists())

    def test_absent_ledger_context_and_reads_never_acknowledge(self):
        self.enroll("--survivor", "2", "--closure-comment", "1", "--provenance", "External rationale unknown")
        path = self.root / "data/owner/repo/cache/watches/pr-1.json"
        original = path.read_bytes()
        calls = self.calls()
        listing = self.attention()
        self.assertTrue(listing["rows"][0]["selectable"])
        self.assertIsInstance(json.loads(listing["rows"][0]["preview"])["response_comments"], int)
        token = listing["rows"][0]["watch_checkpoint"]
        context = self.detail("context", token)
        values = {row["id"]: json.loads(row["preview"]) for row in context["rows"]}
        self.assertEqual(values["survivor"]["number"], 2)
        self.assertEqual(values["closure"]["closure"]["comment_id"], 1)
        self.assertIsNotNone(values["coverage"]["coverage"]["last_successful_check"])
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())
        history = self.detail("history", token)
        self.assertTrue(any(json.loads(row["preview"])["supplied_closure_reference"] for row in history["rows"]))
        closure_note = next(row for row in history["rows"] if row["label"].startswith("comment:1 ·"))
        excerpt = json.loads(closure_note["preview"])
        self.assertIn("comment_excerpt", excerpt)
        self.assertLessEqual(len(excerpt["comment_excerpt"].encode()), 240)
        self.assertEqual(path.read_bytes(), original)
        self.assertEqual(self.calls(), calls)

    def test_catalog_pagination_unavailable_rows_and_changed_progress(self):
        self.enroll()
        directory = self.root / "data/owner/repo/cache/watches"
        for number in range(2, 12):
            (directory / f"pr-{number}.json").write_text("broken")
        first = self.attention("list", "--limit", "5")
        self.assertEqual(len(first["rows"]), 5)
        self.assertFalse(first["rows"][1]["selectable"])
        second = self.attention("list", "--offset", "5", "--limit", "5", "--checkpoint", first["checkpoint"])
        self.assertEqual(second["pagination"]["omitted_before"], 5)
        self.attention("list", "--offset", "5", ok=False)
        self.watch("watch-poll")
        self.attention("list", "--offset", "5", "--checkpoint", first["checkpoint"], ok=False)
        self.detail("context", first["rows"][0]["watch_checkpoint"], ok=False)

    def test_chronological_source_revisions_and_reference_paging(self):
        self.responses["repos/owner/repo/issues/1/comments?per_page=100&page=1"] = dict(data=[dict(comment(2), created_at="2026-09-21T23:00:00-04:00", updated_at="2026-09-21T23:00:00-04:00"), comment()])
        self.enroll()
        self.watch("watch-poll")
        token = self.token()
        result = self.detail("history", token)
        comments = [row for row in result["rows"] if row["label"].startswith("comment:")]
        self.assertEqual([row["label"].split(" · ")[0] for row in comments], ["comment:1", "comment:2"])
        entry = comments[0]["id"]
        refs = self.detail("references", token, "--entry", entry, "--limit", "1")
        self.assertEqual(refs["pagination"]["total"], 4)
        for offset in range(4):
            page = self.detail("references", token, "--entry", entry, "--offset", str(offset), "--limit", "1")
            self.assertEqual(page["rows"][0]["reference"], offset)
            raw = self.source("history", token, entry, "--reference", str(offset))
            self.assertEqual(json.loads(raw["text"])["id"], 1)
        self.source("history", token, entry, "--reference", "4", ok=False)

    def test_unicode_source_fragments_exact_selected_row_and_stale_binding(self):
        body = "é界 new explanation " * 100
        self.responses[self.timeline]["data"] = self.responses[self.timeline]["data"][:1]
        self.responses["repos/owner/repo/issues/1/comments?per_page=100&page=1"] = dict(data=[dict(comment(), body=body), comment(2)])
        self.enroll()
        token = self.token()
        row = next(row for row in self.detail("history", token)["rows"] if row["label"].startswith("comment:1 ·"))
        parts, offset = [], 0
        while True:
            result = self.source("history", token, row["id"], "--byte-offset", str(offset), "--max-bytes", "301")
            parts.append(result["text"])
            self.assertLessEqual(len(result["text"].encode()), 301)
            if result["bytes"]["next_offset"] is None:
                break
            offset = result["bytes"]["next_offset"]
        self.assertEqual(json.loads("".join(parts))["body"], body)
        self.watch("watch-poll")
        self.source("history", token, row["id"], "--byte-offset", str(offset), ok=False)

    def test_operational_failure_stays_visible_and_separate_from_activity(self):
        self.enroll()
        self.responses["repos/owner/repo"] = dict(status=500, data=dict(message="Unavailable"))
        self.watch("watch-poll")
        listing = self.attention()
        self.assertIn("operational", listing["rows"][0]["label"])
        token = listing["rows"][0]["watch_checkpoint"]
        result = self.source("context", token, "poll")
        self.assertEqual(json.loads(result["text"])["status"], "error")

    def test_large_context_is_clipped_with_full_fragments_available(self):
        self.enroll("--provenance", "rationale " * 2000)
        token = self.token()
        closure = next(row for row in self.detail("context", token)["rows"] if row["id"] == "closure")
        self.assertGreater(closure["omitted_bytes"], 0)
        self.assertLessEqual(len(closure["preview"].encode()), 1200)
        raw = self.source("context", token, "closure")
        self.assertIsNotNone(raw["bytes"]["next_offset"])

    def test_corrupt_payload_remains_visible_unselectable_and_never_fetches(self):
        result = self.enroll()
        token = self.token()
        snapshot = result["watch"]["observations"][0]
        manifest = json.loads((self.root / f"data/owner/repo/cache/snapshots/{snapshot}.json").read_text())
        obj = manifest["items"][0]["components"]["comments"]["object"]
        path = next(path for path in (self.root / "data/owner/repo/cache/objects").iterdir() if obj["sha256"] in path.name)
        path.write_text("broken")
        calls = self.calls()
        listing = self.attention()
        self.assertFalse(listing["rows"][0]["selectable"])
        self.assertTrue(listing["rows"][0]["attention"])
        self.detail("history", token, ok=False)
        self.source("context", token, "summary", ok=False)
        self.assertEqual(self.calls(), calls)

    def test_invalid_arguments_and_future_watch_refuse_offline(self):
        self.enroll()
        token = self.token()
        calls = self.calls()
        self.detail("context", token, "--offset", "-1", ok=False)
        self.detail("context", token, "--limit", "51", ok=False)
        self.attention("read", "--number", "1", "--section", "context", ok=False)
        self.source("context", token, "missing", ok=False)
        self.source("context", token, "summary", "--max-bytes", "16385", ok=False)
        self.source("context", token, "summary", "--reference", "1", ok=False)
        path = self.root / "data/owner/repo/cache/watches/pr-1.json"
        value = json.loads(path.read_text())
        value["schema_version"] = 999
        path.write_text(json.dumps(value))
        self.assertFalse(self.attention()["rows"][0]["selectable"])
        self.assertEqual(self.calls(), calls)

    def test_large_history_output_bounded_and_omissions_explicit(self):
        for number in range(3):
            data = [dict(comment(i), body="large dissent " * 1000) for i in range(number * 100 + 1, min(number * 100 + 101, 251))]
            value = dict(data=data)
            if number < 2:
                value["headers"] = {"link": '<https://api.github.com/next>; rel="next"'}
            self.responses[f"repos/owner/repo/issues/1/comments?per_page=100&page={number + 1}"] = value
        self.enroll()
        token = self.token()
        calls = self.calls()
        start = time.monotonic()
        result = self.detail("history", token, "--limit", "5")
        size = len(json.dumps(result).encode())
        self.assertEqual(len(result["rows"]), 5)
        self.assertGreater(result["pagination"]["omitted_after"], 240)
        self.assertLess(size, 12000)
        self.assertEqual(self.calls(), calls)
        print(f"Attention: 250 comments, five-entry page {size} bytes, {time.monotonic()-start:.2f}s offline", file=sys.stderr)

    def test_conflicting_comment_revisions_remain_separately_readable(self):
        self.responses["repos/owner/repo/issues/1/comments?per_page=100&page=1"]["data"][0]["body"] = "Contrary evidence"
        self.enroll()
        token = self.token()
        rows = [row for row in self.detail("history", token)["rows"] if row["label"].startswith("comment:1 ·")]
        self.assertEqual(len(rows), 2)
        bodies = {json.loads(self.source("history", token, row["id"])["text"])["body"] for row in rows}
        self.assertEqual(bodies, {"Contrary evidence", "comment 1"})

    def test_partial_observation_and_unknown_source_dates_stay_explicit(self):
        self.responses["repos/owner/repo/issues/1/comments?per_page=100&page=1"]["data"][0]["created_at"] = "invalid date"
        self.enroll(budget="3")
        token = self.token()
        context = json.loads(self.source("context", token, "coverage")["text"])
        self.assertFalse(context["coverage"]["baseline_complete"])
        self.assertIsNone(context["coverage"]["last_successful_check"])
        self.assertTrue(context["latest_gaps"])
        self.assertTrue(self.detail("history", token)["rows"])

    def test_symlink_watch_stays_visible_unselectable(self):
        self.enroll()
        directory = self.root / "data/owner/repo/cache/watches"
        (directory / "pr-2.json").symlink_to(directory / "pr-1.json")
        result = self.attention()
        self.assertEqual(len(result["rows"]), 2)
        self.assertFalse(result["rows"][1]["selectable"])
        self.assertTrue(result["rows"][1]["attention"])
