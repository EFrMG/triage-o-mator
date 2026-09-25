"""Explicit polling and offline source history in throwaway installs with fake GitHub."""

import json
import subprocess
import time


from support import EvidenceFixture, WatchFixture
from support import comment


class PollTests(WatchFixture):
    python = EvidenceFixture.python

    def test_repeated_polls_deduplicate_comment_endpoints_and_keep_edits(self):
        self.enroll()
        first = self.watch("watch-history")
        self.assertEqual(first["pagination"]["total"], 3)
        identifiers = [row["id"] for row in first["results"]]
        for _ in range(2):
            result = self.watch("watch-poll", "--request-budget", "5")
            self.assertEqual(result["poll"]["status"], "complete")
            self.assertEqual(result["requests"], 5)

        repeated = self.watch("watch-history")
        self.assertEqual([row["id"] for row in repeated["results"]], identifiers)
        comments = next(row for row in repeated["results"] if row["source_id"] == "comment:1")
        self.assertEqual(comments["reference_count"], 6)
        edited = dict(comment(), body="Edited explanation", updated_at="2026-09-23T00:00:00Z")
        self.responses["repos/owner/repo/issues/1/comments?per_page=100&page=1"]["data"] = [edited]
        self.responses[self.timeline]["data"][1] = dict(edited, event="commented")
        self.watch("watch-poll")
        entries = self.watch("watch-history")["results"]
        self.assertEqual(len([row for row in entries if row["source_id"] == "comment:1"]), 2)
        self.assertEqual(entries[0]["id"], identifiers[0])
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())

    def test_partial_poll_requires_bound_resume_and_revalidates_complete_pages(self):
        self.enroll()
        before = len(self.calls())
        partial = self.watch("watch-poll", "--request-budget", "3")
        self.assertEqual(partial["poll"]["status"], "partial")
        self.assertEqual(len(self.calls()) - before, 3)
        self.watch("watch-poll", ok=False)
        self.watch("watch-capture", ok=False)
        self.watch("watch-poll", "--checkpoint", "0" * 64, ok=False)
        self.assertEqual(len(self.calls()) - before, 3)
        resumed = self.watch("watch-poll", "--checkpoint", partial["checkpoint"], "--request-budget", "5")
        self.assertEqual(resumed["poll"]["status"], "complete")
        self.assertEqual(resumed["poll"]["id"], partial["poll"]["id"])
        self.assertEqual(resumed["poll"]["attempts"], 2)
        self.assertEqual(resumed["requests"], 5)
        self.assertIn("repos/owner/repo/issues/1/comments?per_page=100&page=1", [row[-1] for row in self.calls()[before + 3:]])
        self.watch("watch-poll", "--checkpoint", resumed["checkpoint"], ok=False)

    def test_timeline_resume_overlap_edits_and_close_reopen_close(self):
        self.enroll()
        self.responses[self.timeline]["headers"] = {"ETag": '"page-one"', "Link": '<ignored>; rel="next"'}
        page2 = "repos/owner/repo/issues/1/timeline?per_page=100&page=2"
        self.responses[page2] = dict(status=503)
        first = self.watch("watch-poll")
        self.assertEqual(first["poll"]["status"], "partial")
        original_state = first["state"]
        self.responses[page2] = dict(data=[
            dict(id=100, event="reopened", created_at="2026-09-22T03:00:00Z"),
            dict(id=101, event="closed", created_at="2026-09-22T04:00:00Z"),
        ])
        start = len(self.calls())
        result = self.watch("watch-poll", "--checkpoint", first["checkpoint"])
        self.assertEqual(result["state"], original_state)
        self.assertEqual(result["poll"]["status"], "complete")
        calls = self.calls()[start:]
        overlap = next(call for call in calls if call[-1] == self.timeline)
        self.assertIn('If-None-Match: "page-one"', overlap)
        history = self.watch("watch-history")
        keys = [row["source_id"] for row in history["results"]]
        self.assertIn("timeline:reopened:id:100", keys)
        self.assertIn("timeline:closed:id:101", keys)

    def test_resume_detects_changed_page_even_without_summary_revision_change(self):
        self.enroll()
        first = self.watch("watch-poll", "--request-budget", "3")
        changed = dict(comment(), body="Mutable body changed without updated summary")
        self.responses["repos/owner/repo/issues/1/comments?per_page=100&page=1"]["data"] = [changed]
        result = self.watch("watch-poll", "--checkpoint", first["checkpoint"])
        self.assertEqual(result["poll"]["status"], "complete")
        entries = self.watch("watch-history")["results"]
        self.assertEqual(len([row for row in entries if row["source_id"] == "comment:1"]), 2)

    def test_intervening_acquisition_requires_explicit_restart(self):
        self.enroll()
        first = self.watch("watch-poll", "--request-budget", "3")
        self.run_cache("fetch", "pr", "closure-watch", "--mode", "refresh")
        calls = len(self.calls())
        refused = self.watch("watch-poll", "--checkpoint", first["checkpoint"])
        self.assertEqual(refused["poll"]["status"], "error")
        self.assertIn("snapshot changed", refused["poll"]["error"])
        self.assertEqual(len(self.calls()), calls)
        restarted = self.watch("watch-poll", "--restart")
        self.assertEqual(restarted["poll"]["status"], "complete")
        self.assertNotEqual(restarted["poll"]["id"], first["poll"]["id"])

    def test_rate_limit_failure_persists_cooldown_and_successful_check(self):
        enrolled = self.enroll()
        self.responses["repos/owner/repo"] = dict(status=429, headers={"Retry-After": "3600"})
        first = self.watch("watch-poll")
        self.assertEqual(first["poll"]["status"], "error")
        self.assertEqual(first["requests"], 1)
        self.assertEqual(first["last_successful_check"], enrolled["last_successful_check"])
        calls = len(self.calls())
        second = self.watch("watch-poll", "--checkpoint", first["checkpoint"])
        self.assertEqual(second["poll"]["status"], "error")
        self.assertEqual(second["requests"], 0)
        self.assertEqual(len(self.calls()), calls)
        self.assertIn("cooldown", second["poll"]["error"])

    def test_source_fragments_pagination_and_stale_tokens_are_offline(self):
        self.responses["repos/owner/repo/issues/1/comments?per_page=100&page=1"]["data"][0]["body"] = "🐈 " * 500
        self.responses[self.timeline]["data"][1]["body"] = "🐈 " * 500
        self.enroll()
        calls = self.calls()
        first = self.watch("watch-history", "--limit", "1")
        self.watch("watch-history", "--offset", "1", ok=False)
        second = self.watch("watch-history", "--limit", "1", "--offset", "1", "--checkpoint", first["checkpoint"])
        self.assertNotEqual(first["results"][0]["id"], second["results"][0]["id"])
        entry = first["results"][0]["id"]
        refs = self.watch("watch-history", "--entry", entry, "--limit", "1")
        self.assertEqual(refs["pagination"]["total"], 2)
        self.watch("watch-history", "--entry", entry, "--checkpoint", first["checkpoint"], ok=False)
        parts = []
        offset = 0
        while True:
            fragment = self.watch("watch-source", "--entry", entry, "--checkpoint", refs["checkpoint"],
                                  "--max-bytes", "400", "--byte-offset", str(offset))
            parts.append(fragment["source"]["text"])
            self.assertLessEqual(len(parts[-1].encode()), 400)
            if fragment["continuation"] is None:
                break

            offset = fragment["continuation"]["byte_offset"]

        self.assertEqual(json.loads("".join(parts))[0]["body"], "🐈 " * 500)
        self.assertEqual(calls, self.calls())
        self.watch("watch-poll")
        self.watch("watch-history", "--offset", "1", "--checkpoint", first["checkpoint"], ok=False)
        self.watch("watch-source", "--entry", entry, "--checkpoint", refs["checkpoint"], ok=False)

    def test_missing_and_corrupt_evidence_never_falls_back_online(self):
        first = self.enroll()
        snapshot = first["watch"]["observations"][0]
        path = self.root / f"data/owner/repo/cache/snapshots/{snapshot}.json"
        path.unlink()
        calls = self.calls()
        for command in ("watch-history", "watch-status", "watch-poll"):
            self.watch(command, ok=False)

        self.assertEqual(calls, self.calls())

    def test_interruption_after_snapshot_before_watch_commit_keeps_checkpoint_honest(self):
        self.enroll()
        (self.mock / "responses.json").write_text(json.dumps(self.responses))
        self.python("""
import _watch_poll
from _cache import EvidenceCache
from _evidence import repository
cache=EvidenceCache(repository('owner/repo'))
real=_watch_poll.commit
calls=0
def crash(cache,path,watch):
    global calls
    calls+=1
    if calls==2:
        raise RuntimeError('simulated interruption after evidence publication')
    real(cache,path,watch)
_watch_poll.commit=crash
try:
    _watch_poll.poll(cache,1,100)
except RuntimeError:
    pass
""")
        status = self.watch("watch-status")
        self.assertEqual(status["poll"]["status"], "running")
        self.assertIsNone(status["poll"]["snapshot_id"])
        self.assertEqual(status["observation_count"], 1)
        # The initial acquisition can restart safely; its unreferenced snapshot is never claimed as progress.
        resumed = self.watch("watch-poll", "--checkpoint", status["checkpoint"])
        self.assertEqual(resumed["poll"]["status"], "complete")
        self.assertEqual(resumed["observation_count"], 2)

    def test_large_history_is_paged_without_payload_bodies(self):
        self.responses["repos/owner/repo/pulls/1"]["data"]["comments"] = 250
        for page in range(1, 4):
            start = (page - 1) * 100 + 1
            rows = [dict(comment(number), body="Long source " * 500) for number in range(start, min(start + 100, 251))]
            headers = {"Link": '<ignored>; rel="next"'} if page < 3 else {}
            self.responses[f"repos/owner/repo/issues/1/comments?per_page=100&page={page}"] = dict(data=rows, headers=headers)

        self.enroll()
        started = time.monotonic()
        result = self.watch("watch-history", "--limit", "10")
        self.assertEqual(result["pagination"]["total"], 253)
        self.assertEqual(len(result["results"]), 10)
        self.assertNotIn("Long source", json.dumps(result))
        self.assertLess(len(json.dumps(result)), 20000)
        calls = self.calls()
        self.watch("watch-history", "--limit", "10", "--offset", "250", "--checkpoint", result["checkpoint"])
        self.assertEqual(calls, self.calls())
        print(f"Watch history: 250 comments, ten-entry page {len(json.dumps(result))} bytes, {time.monotonic() - started:.2f}s offline", flush=True)

    def test_concurrent_continuations_only_one_consumes_the_checkpoint(self):
        self.enroll()
        partial = self.watch("watch-poll", "--request-budget", "3")
        (self.mock / "responses.json").write_text(json.dumps(self.responses))
        before = len(self.calls())
        args = [str(self.root / "bin/cache"), "watch-poll", "--number", "1", "--checkpoint", partial["checkpoint"]]
        processes = [subprocess.Popen(args, cwd=self.root, env=self.env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True) for _ in range(2)]
        results = [process.communicate(timeout=20) for process in processes]
        self.assertEqual(sorted(process.returncode for process in processes), [0, 1])
        self.assertEqual(len(self.calls()) - before, 5)
        self.assertTrue(any("checkpoint changed" in error for _, error in results))

    def test_invalid_budget_and_future_poll_contract_refuse_before_network(self):
        self.enroll()
        first = self.watch("watch-poll", "--request-budget", "3")
        calls = self.calls()
        self.watch("watch-poll", "--request-budget", "0", "--checkpoint", first["checkpoint"], ok=False)
        self.assertEqual(self.watch("watch-status")["checkpoint"], first["checkpoint"])
        self.python("""
from _cache import EvidenceCache
from _evidence import repository
from _jobs import write_record
from _watch import load,path_for
cache=EvidenceCache(repository('owner/repo'))
watch=load(cache,1)
watch.pop('checksum')
watch['poll']['schema_version']=99
write_record(cache,path_for(cache,1),watch)
""")
        self.watch("watch-poll", "--checkpoint", first["checkpoint"], ok=False)
        self.watch("watch-history", ok=False)
        self.assertEqual(self.calls(), calls)
