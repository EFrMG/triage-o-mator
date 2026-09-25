"""Action history navigation stays bounded and offline, including an absent ledger."""

import json


from support import ClosureStoreFixture
from support import CLAIM, WatchFixture


class ActionHistoryTests(WatchFixture):
    call = ClosureStoreFixture.call
    capture = ClosureStoreFixture.capture
    save = ClosureStoreFixture.save

    def test_catalog_entries_sources_and_separate_watch(self):
        snapshot = self.capture()
        first = self.save(snapshot)["history"]
        second = self.save(snapshot, dict(CLAIM, rationale="Corrected explanation"), checkpoint=first["checksum"],
                           kind="correction", predecessor=first["entries"][0]["id"], reason="Reassessment")["history"]
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())
        calls = self.calls()

        catalog = self.call("from _action_history import listing; print(json.dumps(listing(cache)))")
        self.assertEqual(catalog["rows"][0]["history_checkpoint"], second["checksum"])
        checkpoint = second["checksum"]
        entries = self.call(f"from _action_history import read_page; print(json.dumps(read_page(cache, 1, 'entries', limit=1, checkpoint={checkpoint!r})))")
        self.assertEqual(entries["pagination"]["total"], 2)
        self.assertEqual(entries["rows"][0]["id"], first["entries"][0]["id"])
        self.assertEqual(entries["rows"][0]["omitted_bytes"] > 0, True)
        entry = second["entries"][1]["id"]
        sources = self.call(f"from _action_history import read_page; print(json.dumps(read_page(cache, 1, 'sources', checkpoint={checkpoint!r}, entry={entry!r})))")
        self.assertEqual([row["reference"] for row in sources["rows"]], [0, 1, 2])
        fragment = self.call(f"from _action_history import source; print(json.dumps(source(cache, 1, {entry!r}, 2, checkpoint={checkpoint!r}, max_bytes=20)))")
        self.assertEqual(fragment["requests"], 0)
        self.assertGreater(fragment["bytes"]["omitted_after"], 0)
        context = self.call(f"from _action_history import read_page; print(json.dumps(read_page(cache, 1, checkpoint={checkpoint!r})))")
        self.assertEqual(context["rows"][1]["watch"]["status"], "not-enrolled")
        self.assertEqual(calls, self.calls())

    def test_stale_tokens_and_unavailable_history(self):
        snapshot = self.capture()
        first = self.save(snapshot)["history"]
        before = self.call("from _action_history import listing; print(json.dumps(listing(cache)))")
        path = self.root / "data/owner/repo/external-closures/pr-1.json"
        path.write_bytes(path.read_bytes().replace(b"\n", b"\r\n"))
        changed = self.call("from _action_history import listing; print(json.dumps(listing(cache)))")
        self.assertNotEqual(before["checkpoint"], changed["checkpoint"])
        before = changed
        self.save(snapshot, dict(CLAIM, rationale="Other explanation"), checkpoint=first["checksum"],
                  kind="competing", predecessor=first["entries"][0]["id"], reason="Dissent")
        error = self.call(f"from _action_history import listing; print(json.dumps(listing(cache, checkpoint={before['checkpoint']!r})))", ok=False)
        self.assertIn("catalog changed", error)
        error = self.call(f"from _action_history import read_page; print(json.dumps(read_page(cache, 1, checkpoint={first['checksum']!r})))", ok=False)
        self.assertIn("history changed", error)
        path = self.root / "data/owner/repo/external-closures/pr-1.json"
        path.write_text("bad json")
        unavailable = self.call("from _action_history import listing; print(json.dumps(listing(cache)))")
        self.assertFalse(unavailable["rows"][0]["selectable"])

    def test_watch_link_keeps_its_own_checkpoint(self):
        watch = self.enroll()["watch"]
        saved = self.save(watch["observations"][0])["history"]
        result = self.call(f"from _action_history import read_page; print(json.dumps(read_page(cache, 1, checkpoint={saved['checksum']!r})))")
        link = result["rows"][1]["watch"]
        self.assertEqual(link["status"], "linked")
        self.assertEqual(link["checksum"], watch["checksum"])
        self.assertEqual(result["checkpoint"], saved["checksum"])

    def test_long_original_dissent_is_readable_in_entry_fragments(self):
        snapshot = self.capture()
        first = self.save(snapshot)["history"]
        long_claim = dict(CLAIM, rationale="dissent 🐈 " * 1000)
        second = self.save(snapshot, long_claim, checkpoint=first["checksum"], kind="competing",
                           predecessor=first["entries"][0]["id"], reason="Independent review")["history"]
        entry = second["entries"][1]["id"]
        checkpoint = second["checksum"]
        result = self.call(f"""
from _action_history import source
from _evidence import canonical
parts = []
offset = 0
while True:
    page = source(cache, 1, {entry!r}, -1, checkpoint={checkpoint!r}, byte_offset=offset, max_bytes=4096)
    assert page['bytes']['returned'] <= 4096
    assert 'payloads unaudited' in page['audit_scope']
    parts.append(page['text'])
    offset = page['bytes']['next_offset']
    if offset is None:
        break
print(json.dumps(dict(text=''.join(parts), fragments=len(parts))))
""")
        self.assertEqual(json.loads(result['text']), second['entries'][1])
        self.assertGreater(result['fragments'], 2)

    def test_missing_cache_keeps_unavailable_catalog_member(self):
        import shutil

        self.save(self.capture())
        shutil.rmtree(self.root / "data/owner/repo/cache")
        result = self.call("from _action_history import listing; print(json.dumps(listing(cache)))")
        self.assertEqual(result["pagination"]["total"], 1)
        self.assertFalse(result["rows"][0]["selectable"])
        self.assertFalse((self.root / "data/owner/repo/cache").exists())

    def test_corrupt_sources_refused_but_attributed_entry_remains_readable(self):
        history = self.save(self.capture())["history"]
        entry = history["entries"][0]
        source = entry["observation"]["sources"][2]
        path = self.root / "data/owner/repo/cache/objects" / (source["object"]["sha256"] + ".json")
        path.write_bytes(path.read_bytes() + b" ")
        args = f"cache, 1, {entry['id']!r}, checkpoint={history['checksum']!r}"
        error = self.call(f"from _action_history import source; print(json.dumps(source({args}, reference=2)))", ok=False)
        self.assertIn("checksum", error)
        result = self.call(f"from _action_history import source; print(json.dumps(source({args}, reference=-1)))")
        self.assertIn("Original explanation", result["text"])
        self.assertIn("payloads unaudited", result["audit_scope"])
