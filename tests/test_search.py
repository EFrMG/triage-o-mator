"""Bounded source search against fixed evidence in fake installs only."""

import json
import subprocess
import sys


from support import ChunkFixture, CorpusFixture
from support import AcquisitionFixture


class SearchTests(AcquisitionFixture):
    row = ChunkFixture.row
    cli = ChunkFixture.cli
    fetch_inventory = ChunkFixture.fetch_inventory
    seed = ChunkFixture.seed
    create = CorpusFixture.create
    seed_details = CorpusFixture.seed_details
    run_corpus = CorpusFixture.run_corpus

    def search(self, identifier, query="needle", component="summary", corpus=False, *args, ok=True):
        return self.cli("cache", "search", "--corpus" if corpus else "--snapshot", identifier,
                        "--query", query, "--component", component, *args, ok=ok)

    def test_fixed_search_pagination_query_binding_and_no_writes(self):
        snapshot = self.seed(body="needle 🐈")
        before = len(self.calls())
        first = json.loads(self.search(snapshot, "needle", "summary", False, "--limit", "1").stdout)
        self.assertEqual(first["matched_items"], 1)
        item = first["items"][0]
        self.assertEqual(item["match"]["pointer"], "/body")
        self.assertEqual(item["snapshot_id"], snapshot)
        self.assertEqual(item["status"], "partial")
        self.assertTrue(item["problems"]["summary"])
        second = json.loads(self.search(snapshot, "needle", "summary", False, "--offset", "1", "--limit", "1", "--checkpoint", first["checkpoint"]).stdout)
        self.assertEqual(second["items"][0]["identity"]["number"], 2)
        self.search(snapshot, "changed", "summary", False, "--offset", "1", "--checkpoint", first["checkpoint"], ok=False)
        self.search(snapshot, "needle", "summary", False, "--offset", "1", ok=False)
        self.assertEqual(len(self.calls()), before)
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())
        self.seed(body="replacement")
        self.assertEqual(json.loads(self.search(snapshot).stdout)["matched_items"], 3)

    def test_nonmatches_missing_and_empty_pages_are_distinct(self):
        snapshot = self.seed(body="Needle")
        before = len(self.calls())
        packet = json.loads(self.search(snapshot, "needle", "summary", False, "--limit", "1").stdout)
        self.assertEqual(packet["matched_items"], 0)
        self.assertTrue(packet["items"][0]["available"])
        self.assertIsNotNone(packet["continuation"])
        missing = json.loads(self.search(snapshot, component="diff").stdout)
        self.assertFalse(missing["items"][0]["available"])
        self.assertEqual(missing["items"][0]["problems"]["diff"], ["missing"])
        self.assertEqual(len(self.calls()), before)

    def test_corruption_and_absent_snapshot_fail_without_fetch(self):
        snapshot = self.seed()
        packet = json.loads(self.search(snapshot, query="private").stdout)
        reference = packet["items"][0]["object"]
        path = next((self.root / "data/owner/repo/cache/objects").glob(reference["sha256"] + "*"))
        path.write_text("corrupt")
        before = len(self.calls())
        failed = self.search(snapshot, ok=False)
        self.assertEqual(failed.stdout, "")
        self.search("0" * 64, ok=False)
        self.assertEqual(len(self.calls()), before)

    def test_corpus_pins_and_checkpoint_change(self):
        identifier, snapshot = self.create()
        before = len(self.calls())
        pending = json.loads(self.search(identifier, corpus=True).stdout)
        self.assertTrue(all(not row["available"] for row in pending["items"]))
        self.assertEqual(len(self.calls()), before, "pending members never fall back to inventory or live data")
        self.run_corpus(identifier)
        before = len(self.calls())
        packet = json.loads(self.search(identifier, "Body", "summary", True, "--limit", "1").stdout)
        self.assertEqual(packet["matched_items"], 1)
        self.assertNotEqual(packet["items"][0]["snapshot_id"], snapshot)
        self.search(identifier, "needle", "summary", True, "--checkpoint", pending["checkpoint"], ok=False)
        last = json.loads(self.search(identifier, "Body", "summary", True, "--offset", "1", "--checkpoint", packet["checkpoint"]).stdout)
        self.assertIsNone(last["continuation"])
        self.assertEqual(len(self.calls()), before)

    def test_only_examined_payloads_are_read(self):
        snapshot = self.seed()
        metadata = json.loads(self.cli("cache", "list", "--snapshot", snapshot).stdout)
        reference = metadata["items"][1]["components"]["summary"]["object"]
        path = next((self.root / "data/owner/repo/cache/objects").glob(reference["sha256"] + "*"))
        path.write_text("corrupt second item")
        before = len(self.calls())
        first = json.loads(self.search(snapshot, "private", "summary", False, "--limit", "1").stdout)
        self.assertEqual(first["matched_items"], 1)
        self.search(snapshot, "private", "summary", False, "--offset", "1", "--limit", "1", "--checkpoint", first["checkpoint"], ok=False)
        self.assertEqual(len(self.calls()), before)

    def test_large_unicode_source_bounded_excerpt_and_literal_matching(self):
        snapshot = self.seed(count=1, body="🐈" * 262144 + "[needle].*" + "x" * 1000)
        before = len(self.calls())
        packet = self.search(snapshot, query="[needle].*")
        self.assertLess(len(packet.stdout.encode()), 8000)
        match = json.loads(packet.stdout)["items"][0]["match"]
        self.assertEqual(match["character_offset"], 262144)
        self.assertLessEqual(len(match["excerpt"]), 376)
        self.assertGreater(match["omitted_before"], 0)
        self.assertGreater(match["omitted_after"], 0)
        self.assertEqual(len(self.calls()), before)

    def test_invalid_inputs_and_string_value_locations(self):
        snapshot = self.seed()
        before = len(self.calls())
        for query in ("", "   ", "x" * 257):
            self.search(snapshot, query=query, ok=False)

        for args in (("--limit", "101"), ("--offset", "-1"), ("--max-age", "-1")):
            self.search(snapshot, "private", "summary", False, *args, ok=False)

        script = r'''
import sys
sys.path.insert(0, 'bin')
from _search import first_match
assert first_match('[{"filename":"a/b.py"}]', 'files', 'b.py')['pointer'] == '/0/filename'
assert first_match('[{"url":"https://github.com/a/b/issues/42"}]', 'closing_issues', '/42')['pointer'] == '/0/url'
assert first_match('+needle\r\n', 'diff', 'needle')['excerpt'] == '+needle\r\n'
assert first_match('{"needle":42}', 'summary', 'needle') is None
'''
        result = subprocess.run([sys.executable, "-c", script], cwd=self.root, env=self.env, capture_output=True, text=True, timeout=20)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(len(self.calls()), before)
