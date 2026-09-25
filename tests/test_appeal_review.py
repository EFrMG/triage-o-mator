"""A fabricated closure appeal retains source history through import, attention review and reassessment."""

import json
import subprocess


from support import WatchFixture


class AppealReviewTests(WatchFixture):
    def cli(self, *args, ok=True):
        result = subprocess.run([str(self.root / "bin/cache"), *args], cwd=self.root, env=self.env,
                                capture_output=True, text=True, timeout=30)
        if not ok:
            self.assertNotEqual(result.returncode, 0, result.stdout)

            return result.stderr

        self.assertEqual(result.returncode, 0, result.stderr)

        return json.loads(result.stdout)

    def import_claim(self, snapshot, claim, *extra):
        path = self.root / "claim.json"
        path.write_text(json.dumps(claim))

        return self.cli("closure-import", "--number", "1", "--snapshot", snapshot, "--by", "operator",
                        "--claim", str(path), "--closure-event", "99", "--comment", "1", *extra)

    def test_import_attention_and_reassessment(self):
        original_claim = dict(external=dict(namespace="fabricated-runner", record_id="run-1"), actor=None,
                              run_id=None, rationale="The retained PR appears to cover this change", survivor=2,
                              provenance="unknown", supports=[])
        first_snapshot = self.run_cache("fetch", "pr", "closure-watch", "--mode", "refresh", "--request-budget", "100")["snapshot_id"]
        calls = self.calls()
        imported = self.import_claim(first_snapshot, original_claim)["history"]
        self.assertEqual(self.cli("closure-show", "--number", "1")["watch"]["status"], "not-enrolled")
        self.assertFalse((self.root / "data/owner/repo/cache/watches/pr-1.json").exists())
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())
        self.assertEqual(self.calls(), calls)

        corrected_claim = dict(original_claim, rationale="The runner compared an earlier revision")
        corrected = self.import_claim(first_snapshot, corrected_claim, "--kind", "correction", "--checkpoint",
                                      imported["checksum"], "--predecessor", imported["entries"][0]["id"],
                                      "--reason", "Clarify supplied explanation")["history"]
        dissent_claim = dict(original_claim, rationale="The contributor reports distinct behavior")
        disputed = self.import_claim(first_snapshot, dissent_claim, "--kind", "competing", "--checkpoint",
                                     corrected["checksum"], "--predecessor", imported["entries"][0]["id"],
                                     "--reason", "Retain contributor disagreement")["history"]
        self.assertEqual(len(disputed["entries"]), 3)
        self.assertEqual(disputed["entries"][0], imported["entries"][0])
        action = self.cli("action-list")
        self.assertEqual(action["rows"][0]["history_checkpoint"], disputed["checksum"])
        self.assertEqual(action["requests"], 0)
        action_entries = self.cli("action-read", "--number", "1", "--section", "entries", "--checkpoint", disputed["checksum"])
        self.assertEqual(action_entries["pagination"]["total"], 3)
        self.assertIn(original_claim["rationale"], json.dumps(action_entries))
        self.assertIn(dissent_claim["rationale"], json.dumps(action_entries))
        self.cli("action-read", "--number", "1", "--section", "entries", "--checkpoint", imported["checksum"], ok=False)
        action_source = self.cli("action-source", "--number", "1", "--entry", disputed["entries"][2]["id"],
                                 "--reference", "-1", "--checkpoint", disputed["checksum"])
        self.assertIn("The contributor reports distinct behavior", action_source["text"])
        self.assertEqual(self.calls(), calls)

        enrolled = self.watch("watch-enroll", "--snapshot", first_snapshot, "--by", "operator", "--closure-event", "99",
                              "--closure-comment", "1", "--survivor", "2")
        self.assertIsNone(enrolled["watch"]["provenance"])
        self.responses["repos/owner/repo/issues/1/comments?per_page=100&page=1"]["data"][0].update(
            body="The changes are different; please reconsider", updated_at="2026-09-23T00:00:00Z")
        watch_path = self.root / "data/owner/repo/cache/watches/pr-1.json"
        watch_before_observation = watch_path.read_bytes()
        second_snapshot = self.run_cache("fetch", "pr", "closure-watch", "--mode", "refresh", "--request-budget", "100")["snapshot_id"]
        observed = self.import_claim(second_snapshot, corrected_claim, "--kind", "observation", "--checkpoint",
                                     disputed["checksum"], "--predecessor", corrected["entries"][1]["id"],
                                     "--reason", "Comment source was edited")["history"]
        self.assertEqual(watch_path.read_bytes(), watch_before_observation)
        self.assertEqual(observed["entries"][:3], disputed["entries"])
        self.assertEqual(len(observed["entries"]), 4)
        self.watch("watch-poll", "--restart")
        offline_calls = self.calls()
        denied_calls = self.mock / "denied-gh-calls.log"
        (self.mock / "gh").write_text(f"#!/bin/sh\nprintf '%s\\n' attempted >> '{denied_calls}'\nexit 99\n")
        watch_before_reads = watch_path.read_bytes()
        (self.mock / "gh").chmod(0o755)
        sources = self.cli("action-read", "--number", "1", "--section", "sources", "--entry", observed["entries"][-1]["id"],
                           "--checkpoint", observed["checksum"])
        self.assertEqual(sources["pagination"]["total"], 3)
        self.assertEqual(sources["requests"], 0)
        source = self.cli("action-source", "--number", "1", "--entry", observed["entries"][-1]["id"],
                          "--reference", "2", "--checkpoint", observed["checksum"])
        self.assertIn("The changes are different; please reconsider", source["text"])

        attention = self.cli("attention-list")
        token = attention["rows"][0]["watch_checkpoint"]
        history = self.cli("attention-read", "--number", "1", "--section", "history", "--checkpoint", token)
        bodies = []
        for comment_row in (row for row in history["rows"] if row["label"].startswith("comment:1")):
            refs = self.cli("attention-read", "--number", "1", "--section", "references", "--entry", comment_row["id"],
                            "--checkpoint", token)
            bodies.extend(json.loads(self.cli("attention-source", "--number", "1", "--section", "history", "--entry", comment_row["id"],
                                              "--reference", str(i), "--checkpoint", token)["text"])["body"]
                          for i in range(refs["pagination"]["total"]))
        self.assertIn("comment 1", bodies)
        self.assertIn("The changes are different; please reconsider", bodies)

        # Reading the appeal is the whole local workflow: nothing here records a verdict or changes the watch.
        self.assertEqual(watch_path.read_bytes(), watch_before_reads)
        self.assertTrue(attention["rows"][0]["attention"])
        self.assertGreater(json.loads(attention["rows"][0]["preview"])["response_comments"], 0)
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())
        self.assertEqual(self.calls(), offline_calls)
        self.assertFalse(denied_calls.exists())
        saved_closure = json.loads((self.root / "data/owner/repo/external-closures/pr-1.json").read_text())
        self.assertEqual(saved_closure, observed)
