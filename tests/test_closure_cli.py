"""Real closure CLI integration over disposable evidence; all offline operations deny GitHub."""

import json
import subprocess


from support import WatchFixture


class ClosureCLITests(WatchFixture):
    def closure(self, action, *args, ok=True):
        result = subprocess.run([str(self.root / "bin/cache"), "closure-" + action, "--number", "1", *args],
                                cwd=self.root, env=self.env, capture_output=True, text=True, timeout=20)
        if not ok:
            self.assertNotEqual(result.returncode, 0, result.stdout)
            return result.stderr

        self.assertEqual(result.returncode, 0, result.stderr)
        return json.loads(result.stdout)

    def test_import_retry_correction_and_watch_are_separate(self):
        watched = self.enroll("--closure-event", "99", "--closure-comment", "1")
        snapshot = watched["watch"]["observations"][-1]
        watch_path = next((self.root / "data/owner/repo/cache").rglob("watches/pr-1.json"))
        original_watch = watch_path.read_bytes()
        claim = dict(external=None, actor=None, run_id=None, rationale="Original explanation", survivor=None,
                     provenance="unknown", supports=[2])
        claim_path = self.root / "claim.json"
        claim_path.write_text(json.dumps(claim))
        args = ["--snapshot", snapshot, "--by", "reviewer", "--closure-event", "99", "--comment", "1", "--claim", str(claim_path)]
        calls = self.calls()
        (self.mock / "gh").write_text("#!/bin/sh\nexit 91\n")

        first = self.closure("import", *args)
        self.assertEqual(first["status"], "imported")
        history = first["history"]
        self.assertEqual(self.closure("import", *args)["history"], history)
        self.assertEqual(self.closure("show")["watch"]["checksum"], watched["watch"]["checksum"])

        claim["rationale"] = "Competing explanation"
        claim_path.write_text(json.dumps(claim))
        self.closure("import", *args, ok=False)
        revised = self.closure("import", *args, "--kind", "competing", "--predecessor", history["entries"][0]["id"],
                               "--reason", "Retain dissent", "--checkpoint", history["checksum"])["history"]
        self.assertEqual([entry["claim"]["rationale"] for entry in revised["entries"]],
                         ["Original explanation", "Competing explanation"])
        self.assertEqual(self.closure("show")["history"], revised)
        self.assertEqual(watch_path.read_bytes(), original_watch)
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())
        self.assertEqual(self.calls(), calls)

    def test_duplicate_claim_keys_are_rejected_before_persistence(self):
        snapshot = self.run_cache("fetch", "pr", "closure-watch", "--mode", "refresh")["snapshot_id"]
        claim_path = self.root / "claim.json"
        claim_path.write_text('{"provenance":"unknown","provenance":"manual"}')
        error = self.closure("import", "--snapshot", snapshot, "--by", "reviewer", "--claim", str(claim_path), ok=False)
        self.assertIn("duplicate JSON field", error)
        self.assertFalse((self.root / "data/owner/repo/external-closures").exists())
