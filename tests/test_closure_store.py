"""Closure persistence uses disposable installs and explicit retained evidence; offline calls deny gh."""

import json


from support import CLAIM, ClosureStoreFixture


class ClosureStoreTests(ClosureStoreFixture):
    def test_absent_ledger_exact_repeat_and_selected_sources(self):
        snapshot = self.capture()
        calls = self.calls()
        first = self.save(snapshot)
        path = self.root / "data/owner/repo/external-closures/pr-1.json"
        before = path.read_bytes()
        repeat = self.save(snapshot)
        self.assertEqual(repeat["status"], "unchanged")
        self.assertEqual(before, path.read_bytes())
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())
        shown = self.call("print(json.dumps(inspect(cache, 1)))")
        self.assertEqual(shown["history"], first["history"])
        self.assertEqual(shown["observations"][0]["sources"][2]["body"], "comment 1")
        self.assertEqual(shown["watch"]["status"], "not-enrolled")
        self.assertEqual(calls, self.calls())

    def test_corrections_conflicts_and_watch_case_bytes_untouched(self):
        watch = self.enroll()
        snapshot = watch["watch"]["observations"][0]
        watched = self.root / "data/owner/repo/cache/watches/pr-1.json"
        ledger = self.root / "data/owner/repo/ledger.jsonl"
        ledger.write_text('preserve arbitrary existing ledger bytes\n')
        before = watched.read_bytes(), ledger.read_bytes()
        first = self.save(snapshot)["history"]
        changed = dict(CLAIM, rationale="Dissent")
        self.assertIn("checkpoint changed", self.save(snapshot, changed, ok=False))
        self.assertIn("conflicts", self.save(snapshot, changed, checkpoint=first["checksum"], ok=False))
        corrected = self.save(snapshot, changed, checkpoint=first["checksum"], kind="correction", predecessor=first["entries"][0]["id"], reason="Fix attribution")["history"]
        self.assertEqual(corrected["entries"][0], first["entries"][0])
        self.assertIn("checkpoint changed", self.save(snapshot, dict(CLAIM, rationale="Other"), checkpoint=first["checksum"], kind="competing", predecessor=first["entries"][0]["id"], reason="Disagree", ok=False))
        shown = self.call("print(json.dumps(inspect(cache, 1)))")
        self.assertEqual(shown["watch"]["checksum"], watch["watch"]["checksum"])
        self.assertEqual(before, (watched.read_bytes(), ledger.read_bytes()))

    def test_summary_unknown_historical_open_and_merged(self):
        snapshot = self.capture()
        result = self.call(f"print(json.dumps(import_closure(cache, 1, {snapshot!r}, 'operator', {CLAIM!r})))")
        self.assertIsNone(result["history"]["entries"][0]["observation"]["operation_id"])
        for changes in (dict(state="open", closed_at=None), dict(state="closed", merged=True)):
            snapshot = self.capture(**changes)
            result = self.save(snapshot, dict(CLAIM, external=None), checkpoint=result["history"]["checksum"])
            self.assertEqual(result["history"]["entries"][-1]["observation"]["state"], "merged" if changes.get("merged") else "open")

    def test_missing_and_corrupt_evidence_reader(self):
        snapshot = self.capture()
        history = self.save(snapshot)["history"]
        ref = history["entries"][0]["observation"]["sources"][-1]["object"]
        path = self.root / "data/owner/repo/cache/objects" / (ref["sha256"] + ".json")
        original = path.read_bytes()
        path.unlink()
        shown = self.call("print(json.dumps(inspect(cache, 1)))")
        self.assertEqual(shown["observations"][0]["status"], "unavailable")
        path.write_bytes(original + b" ")
        self.assertIn("checksum", self.call("print(json.dumps(inspect(cache, 1)))", ok=False))

    def test_nonclosure_event_and_unknown_ids_rejected(self):
        self.responses[self.timeline]["data"].append(dict(id=100, event="reopened", created_at="2026-09-22T00:00:00Z"))
        snapshot = self.capture()
        for args in (dict(event=100), dict(event=999), dict(event=99, comments=[999])):
            error = self.call(f"print(json.dumps(import_closure(cache, 1, {snapshot!r}, 'operator', {CLAIM!r}, **{args!r})))", ok=False)
            self.assertTrue("not closed" in error or "absent or ambiguous" in error)

    def test_atomic_publication_failure_and_retry(self):
        snapshot = self.capture()
        first = self.save(snapshot)["history"]
        args = dict(event=99, checkpoint=first["checksum"], kind="competing", predecessor=first["entries"][0]["id"], reason="Dissent")
        claim = dict(CLAIM, rationale="Another")
        path = self.root / "data/owner/repo/external-closures/pr-1.json"
        before = path.read_bytes()
        code = f"from unittest.mock import patch\nwith patch('_storage.os.replace', side_effect=OSError('publication failed')):\n    import_closure(cache, 1, {snapshot!r}, 'operator', {claim!r}, **{args!r})"
        self.assertIn("publication failed", self.call(code, ok=False))
        self.assertEqual(before, path.read_bytes())
        result = self.call(f"print(json.dumps(import_closure(cache, 1, {snapshot!r}, 'operator', {claim!r}, **{args!r})))")
        self.assertEqual(len(result["history"]["entries"]), 2)

    def test_edited_sources_preserve_original_revision(self):
        first_snapshot = self.capture()
        first = self.save(first_snapshot)["history"]
        self.responses["repos/owner/repo/issues/1/comments?per_page=100&page=1"]["data"][0].update(body="Edited explanation", updated_at="2026-09-23T00:00:00Z")
        second_snapshot = self.capture()
        second = self.save(second_snapshot, checkpoint=first["checksum"], kind="observation", predecessor=first["entries"][0]["id"], reason="Source edited")["history"]
        self.assertEqual(second["entries"][0], first["entries"][0])
        shown = self.call("print(json.dumps(inspect(cache, 1)))")
        self.assertEqual([row["sources"][2]["body"] for row in shown["observations"]], ["comment 1", "Edited explanation"])

    def test_close_reopen_close_retains_separate_operations(self):
        first = self.save(self.capture())["history"]
        self.responses[self.timeline]["data"].extend([dict(id=100, event="reopened", created_at="2026-09-22T00:00:00Z"), dict(id=101, event="closed", created_at="2026-09-23T00:00:00Z")])
        snapshot = self.capture()
        second = self.call(f"print(json.dumps(import_closure(cache, 1, {snapshot!r}, 'operator', {dict(CLAIM, external=None)!r}, event=101, checkpoint={first['checksum']!r})))")["history"]
        self.assertNotEqual(second["entries"][0]["observation"]["operation_id"], second["entries"][1]["observation"]["operation_id"])

    def test_partial_discussion_keeps_gap_without_fetch(self):
        self.responses[self.timeline] = dict(status=403, data=dict(message="Unavailable"))
        snapshot = self.capture()
        before = self.calls()
        result = self.call(f"print(json.dumps(import_closure(cache, 1, {snapshot!r}, 'operator', {CLAIM!r})))")
        self.assertTrue(any(gap["component"] == "timeline" for gap in result["history"]["entries"][0]["observation"]["gaps"]))
        self.assertEqual(before, self.calls())

    def test_foreign_item_and_summary_semantics_rejected(self):
        snapshot = self.capture()
        # Publish a correctly checksummed artifact with a semantically foreign summary; byte verification alone cannot catch it.
        code = f"""from _evidence import artifact_ref, seal_snapshot
manifest = cache.load({snapshot!r})
record = manifest['items'][0]
component = record['components']['summary']
raw = json.loads(cache.read_object(component['object']))
raw['html_url'] = 'https://github.com/foreign/repo/pull/1'
payload = canonical(raw)
component['object'] = artifact_ref(payload)
manifest = seal_snapshot(manifest)
cache.publish(manifest, {{component['object']['sha256'] + '.json': payload}})
print(json.dumps(manifest['snapshot_id']))
"""
        forged = self.call(code)
        self.assertIn("namespace", self.save(forged, ok=False))

    def test_unsupported_manifest_and_symlink_storage_refused(self):
        snapshot = self.capture()
        path = self.root / "data/owner/repo/cache/snapshots" / (snapshot + ".json")
        original = path.read_text()
        value = json.loads(original)
        value["schema_version"] = 999
        path.write_text(json.dumps(value))
        self.save(snapshot, ok=False)
        path.write_text(original)
        (self.root / "data/owner/repo/external-closures").symlink_to(self.mock, target_is_directory=True)
        self.assertIn("symlinks", self.save(snapshot, ok=False))

    def test_competing_writers_only_one_guarded_append(self):
        snapshot = self.capture()
        first = self.save(snapshot)["history"]
        args = dict(event=99, checkpoint=first["checksum"], kind="competing", predecessor=first["entries"][0]["id"], reason="Disagree")
        code = f"""from concurrent.futures import ProcessPoolExecutor

def write(index):
    try:
        import_closure(cache, 1, {snapshot!r}, 'operator', dict({CLAIM!r}, rationale=str(index)), **{args!r})
        return 'saved'
    except ValueError as error:
        return str(error)

if __name__ == '__main__':
    with ProcessPoolExecutor(max_workers=2) as pool:
        print(json.dumps(list(pool.map(write, [1, 2]))))
"""
        result = self.call(code)
        self.assertEqual(result.count("saved"), 1)
        self.assertTrue(any("checkpoint changed" in value for value in result))
        self.assertEqual(len(self.call("print(json.dumps(load(cache, 1)))")["entries"]), 2)

    def test_foreign_descriptor_is_rejected_and_pr_comment_url_accepted(self):
        self.responses["repos/owner/repo/issues/1/comments?per_page=100&page=1"]["data"][0]["html_url"] = "https://github.com/owner/repo/pull/1#issuecomment-1"
        snapshot = self.capture()
        self.save(snapshot)
        code = f"""from _evidence import seal_snapshot
manifest = cache.load({snapshot!r})
manifest['items'][0]['components']['timeline']['source']['resource'] = 'repos/foreign/repo/issues/999/timeline'
manifest = seal_snapshot(manifest)
cache.publish(manifest, {{}})
print(json.dumps(manifest['snapshot_id']))
"""
        forged = self.call(code)
        self.assertIn("source scope", self.save(forged, ok=False))

    def test_unselected_component_corruption_does_not_expand_audit(self):
        snapshot = self.capture()
        manifest = json.loads((self.root / "data/owner/repo/cache/snapshots" / (snapshot + ".json")).read_text())
        ref = manifest["items"][0]["components"]["comments"]["object"]
        (self.root / "data/owner/repo/cache/objects" / (ref["sha256"] + ".json")).write_text('corrupt')
        result = self.call(f"print(json.dumps(import_closure(cache, 1, {snapshot!r}, 'operator', {CLAIM!r}, event=99)))")
        self.assertEqual(result["audit"]["components"], ["summary", "timeline"])
        self.assertIn("unaudited", result["audit"]["scope"])

    def test_post_replacement_sync_failure_exact_retry(self):
        snapshot = self.capture()
        code = f"from unittest.mock import patch\nwith patch('_storage.sync_directory', side_effect=OSError('durability uncertain')):\n    import_closure(cache, 1, {snapshot!r}, 'operator', {CLAIM!r}, event=99, comments=[1])"
        self.assertIn("durability uncertain", self.call(code, ok=False))
        path = self.root / "data/owner/repo/external-closures/pr-1.json"
        before = path.read_bytes()
        result = self.save(snapshot)
        self.assertEqual(result["status"], "unchanged")
        self.assertEqual(before, path.read_bytes())

    def test_large_selected_component_keeps_full_source_and_bounded_selection(self):
        from test_acquisition import comment

        rows = [dict(comment(index), body="Retained text " * 80) for index in range(1, 5001)]
        snapshot = self.capture()
        (self.root / "large-rows.json").write_text(json.dumps(rows))
        snapshot = self.call(f"""from _evidence import artifact_ref, seal_snapshot
from pathlib import Path
manifest = cache.load({snapshot!r})
component = manifest['items'][0]['components']['comments']
payload = Path('large-rows.json').read_text()
component.update(object=artifact_ref(payload), received_count=5000, expected_count=5000)
manifest = seal_snapshot(manifest)
cache.publish(manifest, {{component['object']['sha256'] + '.json': payload}})
print(json.dumps(manifest['snapshot_id']))
""")
        code = f"""import time
start = time.perf_counter()
result = import_closure(cache, 1, {snapshot!r}, 'operator', {CLAIM!r}, event=99, comments=[5000])
shown = inspect(cache, 1)
print(json.dumps(dict(seconds=time.perf_counter()-start, component_bytes=result['history']['entries'][0]['observation']['sources'][2]['object']['bytes'], selected=len(shown['observations'][0]['sources']), body=shown['observations'][0]['sources'][2]['body'])))
"""
        result = self.call(code)
        self.assertEqual(result["selected"], 3)
        self.assertEqual(result["body"], rows[-1]["body"])
        self.assertGreater(result["component_bytes"], 5000000)
        self.assertLess(result["seconds"], 15)
        self.measurement = result
