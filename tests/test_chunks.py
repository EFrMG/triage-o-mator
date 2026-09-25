"""Offline metadata discovery and bounded immutable component reads in throwaway installs."""

import json


from support import ChunkFixture


class ChunkTests(ChunkFixture):
    def test_listing_is_paginated_without_source_text(self):
        snapshot = self.seed()
        before = len(self.calls())
        result = self.cli("cache", "list", "--snapshot", snapshot, "--limit", "2")
        data = json.loads(result.stdout)
        self.assertNotIn("private source", result.stdout)
        self.assertEqual(len(data["items"]), 2)
        self.assertEqual(data["pagination"]["next_offset"], 2)
        last = json.loads(self.cli("cache", "list", "--snapshot", snapshot, "--offset", "2").stdout)
        self.assertIsNone(last["pagination"]["next_offset"])
        self.assertEqual(last["items"][0]["identity"]["number"], 3)
        self.assertEqual(len(self.calls()), before)

    def test_unicode_fragments_reassemble_without_losing_bytes(self):
        body = "🐈 café\r\n" * 100
        snapshot = self.seed(body=body)
        before = len(self.calls())
        offset, pieces = 0, []
        while True:
            data = json.loads(self.chunk(snapshot, "--max-bytes", "501", "--byte-offset", str(offset)).stdout)
            self.assertLessEqual(len(data["text"].encode("utf-8")), 501)
            self.assertTrue(data["problems"]["summary"])
            self.assertEqual(data["snapshot_id"], snapshot)
            pieces.append(data["text"])
            if data["continuation"] is None:
                break

            offset = data["continuation"]["byte_offset"]

        self.assertEqual(json.loads("".join(pieces))["body"], body)
        self.assertEqual(len(self.calls()), before)

    def test_missing_component_and_invalid_windows_never_fetch(self):
        snapshot = self.seed()
        before = len(self.calls())
        missing = json.loads(self.cli("cache", "chunk", "--snapshot", snapshot, "--kind", "pr", "--number", "1", "--component", "comments").stdout)
        self.assertFalse(missing["available"])
        self.assertEqual(missing["problems"]["comments"], ["missing"])
        for args in (("--limit", "0"), ("--limit", "101"), ("--offset", "-1"), ("--offset", "2"), ("--max-bytes", "65537"), ("--byte-offset", "999999")):
            self.chunk(snapshot, *args, ok=False)

        self.chunk("0" * 64, ok=False)
        self.assertEqual(len(self.calls()), before)

    def test_listing_only_verifies_manifest_but_read_checks_payload(self):
        snapshot = self.seed()
        listing = json.loads(self.cli("cache", "list", "--snapshot", snapshot).stdout)
        ref = listing["items"][0]["components"]["summary"]["object"]
        candidates = list((self.root / "data/owner/repo/cache").rglob(ref["sha256"] + "*"))
        self.assertEqual(len(candidates), 1)
        candidates[0].write_text("corrupt")
        self.cli("cache", "list", "--snapshot", snapshot)
        self.chunk(snapshot, ok=False)

    def test_fixed_snapshot_remains_selected_after_refresh(self):
        snapshot = self.seed(body="old body")
        self.seed(body="new body")
        result = json.loads(self.chunk(snapshot).stdout)
        self.assertEqual(json.loads(result["text"])["body"], "old body")

    def test_large_single_body_has_bounded_fragments(self):
        import time

        snapshot = self.seed(count=1, body="🐈" * 262144)
        before = len(self.calls())
        started = time.monotonic()
        offset = 0
        for _ in range(5):
            packet = json.loads(self.chunk(snapshot, "--byte-offset", str(offset)).stdout)
            self.assertLessEqual(len(packet["text"].encode("utf-8")), 16384)
            self.assertGreater(packet["bytes"]["total"], 1048576)
            self.assertGreater(packet["bytes"]["omitted_after"], 0)
            offset = packet["continuation"]["byte_offset"]

        self.assertEqual(len(self.calls()), before)
        print(f"Large-body benchmark: 1 MiB UTF-8 body, five <=16 KiB fragments in {time.monotonic() - started:.2f}s, zero requests; full selected object verified each time", flush=True)
