"""Offline evidence contracts, publication, and recovery in isolated installs."""

import copy
import json
import subprocess
import sys
import unittest


from support import EvidenceFixture, ROOT, example
sys.path.insert(0, str(ROOT / "bin"))
try:
    from _evidence import artifact_ref, component_problems, coverage, object_name, repository, same_item, same_repository, seal_snapshot, validate_payload, validate_snapshot
finally:
    sys.path.pop(0)


class EvidenceContractTests(unittest.TestCase):
    def test_complete_does_not_mean_fresh_or_authorized(self):
        manifest, _ = example()
        sealed = seal_snapshot(manifest)
        self.assertTrue(coverage(sealed)["complete"])
        component = sealed["items"][0]["components"]["files"]
        revision = dict(sealed["items"][0]["revision"], head_sha="c" * 40)
        self.assertIn("head_sha changed or unknown", component_problems("files", component, revision, "2026-09-21T12:03:00Z", 300))
        self.assertIn("observation outside freshness window", component_problems("files", component, component["revision"], "2026-09-22T12:03:00Z", 300))
        self.assertNotIn("reviewed", sealed)

    def test_partial_failed_and_empty_are_different(self):
        manifest, _ = example()
        component = manifest["items"][0]["components"]["files"]
        for status in ("partial", "failed", "unavailable"):
            component.update(status=status, error="pagination stopped", pagination_complete=False)
            self.assertFalse(coverage(seal_snapshot(manifest))["complete"])

        component.update(status="complete", error=None, pagination_complete=True, expected_count=0, received_count=0, object=artifact_ref("[]"))
        self.assertTrue(coverage(seal_snapshot(manifest))["complete"], "verified empty evidence is not missing evidence")

    def test_incomplete_cannot_claim_complete(self):
        for change in (
            dict(pagination_complete=False), dict(received_count=0), dict(truncated=True),
            dict(object=None), dict(error="truncated"), dict(revision=dict(updated_at=None, base_sha=None, head_sha=None)),
        ):
            with self.subTest(change=change):
                manifest, _ = example()
                manifest["items"][0]["components"]["files"].update(change)
                with self.assertRaises(ValueError):
                    seal_snapshot(manifest)

    def test_scope_requires_every_requested_component(self):
        manifest, _ = example()
        manifest["requested_components"].append("comments")
        with self.assertRaises(ValueError):
            seal_snapshot(manifest)

        manifest, _ = example()
        manifest["items"].append(copy.deepcopy(manifest["items"][0]))
        with self.assertRaises(ValueError):
            seal_snapshot(manifest)

    def test_not_applicable_is_not_a_missing_pr_diff(self):
        manifest, _ = example()
        component = manifest["items"][0]["components"]["files"]
        component.update(status="not_applicable", source=None, fetched_at=None, object=None, expected_count=None, received_count=None, pagination_complete=None)
        with self.assertRaises(ValueError):
            seal_snapshot(manifest)

        manifest["items"][0]["identity"]["kind"] = "issue"
        self.assertTrue(coverage(seal_snapshot(manifest))["complete"])

    def test_schema_hash_reference_and_timestamp_validation(self):
        manifest, _ = example()
        sealed = seal_snapshot(manifest)
        sealed["completed_at"] = "2026-09-21T13:00:00Z"
        with self.assertRaisesRegex(ValueError, "checksum"):
            validate_snapshot(sealed)

        for version in (2, True, "1"):
            manifest["schema_version"] = version
            with self.assertRaisesRegex(ValueError, "unsupported"):
                seal_snapshot(manifest)

        manifest, _ = example()
        manifest["completed_at"] = "2026-09-21T12:02:00"
        with self.assertRaises(ValueError):
            seal_snapshot(manifest)

        for sha in ("../ledger.jsonl", "/tmp/other", "g" * 64):
            with self.assertRaises(ValueError):
                object_name(dict(sha256=sha, bytes=0, format="json"))

    def test_cached_components_keep_original_observation_times(self):
        manifest, _ = example()
        manifest["started_at"] = "2026-09-21T12:02:00Z"
        self.assertTrue(coverage(seal_snapshot(manifest))["complete"])

    def test_repository_and_item_aliases_must_be_reconciled(self):
        original = repository("owner/repo", database_id=42)
        for change in (dict(host="elsewhere.example"), dict(full_name="owner/Repo"), dict(full_name="new/repo"), dict(database_id=43), dict(database_id=None)):
            with self.subTest(change=change), self.assertRaises(ValueError):
                same_repository(original, dict(original, **change))

        same_repository(repository("owner/repo"), original)
        item = dict(kind="pr", number=1, database_id=123, node_id=None)
        for change in (dict(number=2), dict(kind="issue"), dict(database_id=999)):
            with self.assertRaises(ValueError):
                same_item(item, dict(item, **change))

    def test_payload_counts_states_and_nonfinite_json_are_checked(self):
        manifest, _ = example()
        component = manifest["items"][0]["components"]["files"]
        component["object"] = artifact_ref("[]")
        with self.assertRaisesRegex(ValueError, "received_count"):
            validate_payload("files", component, "pr", "[]")

        for state in ("unknown", "unavailable", "deleted", "transferred", "open", "closed", "merged"):
            payload = json.dumps(dict(state=state))
            component["object"] = artifact_ref(payload)
            validate_payload("summary", component, "pr", payload)
            if state == "merged":
                with self.assertRaises(ValueError):
                    validate_payload("summary", component, "issue", payload)

        for value in ("NaN", "Infinity", "-Infinity"):
            with self.assertRaises(ValueError):
                artifact_ref(value)

    def test_duplicate_stable_ids_under_different_numbers_are_refused(self):
        manifest, _ = example()
        duplicate = copy.deepcopy(manifest["items"][0])
        duplicate["identity"]["number"] = 2
        manifest["items"].append(duplicate)
        with self.assertRaisesRegex(ValueError, "stable item ID"):
            seal_snapshot(manifest)


class EvidenceCacheTests(EvidenceFixture):
    def test_status_and_dry_run_do_not_write_or_fetch(self):
        before = sorted(str(path.relative_to(self.root)) for path in self.root.rglob("*"))
        self.assertEqual(json.loads(self.run_cli("cache", "status"))["status"], "not-initialized")
        self.assertEqual(json.loads(self.run_cli("cache", "init", "--dry-run"))["status"], "would-initialize")
        # Python may create bytecode on import; no install data or configuration may change.
        after = sorted(str(path.relative_to(self.root)) for path in self.root.rglob("*") if "__pycache__" not in path.parts)
        self.assertEqual([name for name in before if "__pycache__" not in name], after)
        self.assertFalse((self.mock / "gh_calls.log").exists())

    def test_init_is_idempotent_and_identity_must_be_bound(self):
        self.run_cli("cache", "init")
        cache_path = self.root / "data/owner/repo/cache"
        before = (cache_path / "cache.json").read_bytes()
        self.assertEqual(json.loads(self.run_cli("cache", "init"))["status"], "ready")
        self.assertEqual((cache_path / "cache.json").read_bytes(), before)
        result = self.python("""
from _cache import EvidenceCache
from _evidence import repository, seal_snapshot
from support import example
manifest, payloads = example()
EvidenceCache(repository('owner/repo')).publish(seal_snapshot(manifest), payloads)
""", ok=False)
        self.assertIn("unverified", result.stderr)

    def test_snapshot_reuse_validation_and_no_ledger_mutation(self):
        result = self.python(self.setup_code() + """
first = cache.publish(manifest, payloads)
assert cache.load(first) == manifest
assert cache.publish(manifest, {}) == first
second = seal_snapshot(dict(manifest, completed_at='2026-09-21T12:03:00Z'))
cache.publish(second, {})
assert len(list(cache.path('objects').glob('*.json'))) == 1
print(first)
""")
        snapshot_id = result.stdout.strip()
        self.assertEqual(json.loads(self.run_cli("cache", "show", snapshot_id))["snapshot_id"], snapshot_id)
        status = json.loads(self.run_cli("cache", "status"))
        self.assertEqual(len(status["snapshots"]), 2)
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())
        self.assertFalse((self.mock / "gh_calls.log").exists())

    def test_storage_measurement_and_capacity_preserve_saved_evidence(self):
        self.python(self.setup_code() + """
from unittest.mock import patch
from _cache import CacheCapacityError
from _evidence import artifact_ref
import _cache
initial = cache.usage()
assert initial['categories']['objects'] == 0
assert initial['categories']['snapshots'] == 0
assert initial['limit_bytes'] == 5_000_000_000
with patch.object(_cache, 'DATASET_RESERVE_BYTES', 0), patch.object(_cache, 'DATASET_LIMIT_BYTES', initial['total_bytes'] + 10):
    try:
        cache.publish(manifest, payloads)
    except CacheCapacityError:
        pass
    else:
        raise AssertionError('over-budget snapshot was published')
assert not list(cache.path('snapshots').glob('*.json'))
assert not list(cache.path('objects').glob('*'))
cache.publish(manifest, payloads)
saved = cache.usage()
assert saved['categories']['objects'] > 0
assert saved['categories']['snapshots'] > 0
assert saved['total_bytes'] == sum(saved['categories'].values())
assert cache.load(manifest['snapshot_id']) == manifest
page = '[{\"id\":123}]\\n'
reference = artifact_ref(page)
with patch.object(_cache, 'DATASET_RESERVE_BYTES', 0), patch.object(_cache, 'DATASET_LIMIT_BYTES', cache._budget_used() + 1):
    try:
        cache.store_object(reference, page)
    except CacheCapacityError:
        pass
    else:
        raise AssertionError('over-budget page was saved')
assert not cache.object_path(reference).exists()
assert cache.load(manifest['snapshot_id']) == manifest
assert cache.usage()['total_bytes'] == saved['total_bytes']
""")

    def test_interrupted_publication_has_no_visible_snapshot_and_retry_reuses_objects(self):
        self.python(self.setup_code() + """
from unittest.mock import patch
import _storage
original = _storage.os.replace
def interrupted(source, target):
    if target.parent.name == 'snapshots':
        raise OSError('injected interruption before manifest publication')
    return original(source, target)
try:
    with patch.object(_storage.os, 'replace', interrupted):
        cache.publish(manifest, payloads)
except OSError:
    pass
else:
    raise AssertionError('interruption was not injected')
assert cache.status()['snapshots'] == []
assert len(list(cache.path('objects').glob('*.json'))) == 1
cache.publish(manifest, {})
assert cache.load(manifest['snapshot_id']) == manifest
""")

    def test_corrupt_missing_or_symlinked_objects_are_not_evidence(self):
        self.python(self.setup_code() + """
cache.publish(manifest, payloads)
ref = manifest['items'][0]['components']['files']['object']
path = cache.object_path(ref)
original = path.read_text()
path.write_text('[]')
try:
    cache.load(manifest['snapshot_id'])
except ValueError:
    pass
else:
    raise AssertionError('corrupt evidence accepted')
path.unlink()
try:
    cache.load(manifest['snapshot_id'])
except FileNotFoundError:
    pass
else:
    raise AssertionError('missing evidence accepted')
outside = cache.root.parent / 'not-evidence.json'
outside.write_text(original)
path.symlink_to(outside)
try:
    cache.load(manifest['snapshot_id'])
except ValueError:
    pass
else:
    raise AssertionError('symlink evidence accepted')
""")

    def test_unknown_versions_and_foreign_repository_refuse_writes(self):
        self.run_cli("cache", "init")
        cache_path = self.root / "data/owner/repo/cache"
        metadata = json.loads((cache_path / "cache.json").read_text())
        for change in (dict(schema_version=2), dict(repository=repository("owner/repo", host="enterprise.example")), dict(repository=repository("owner/Repo"))):
            (cache_path / "cache.json").write_text(json.dumps(dict(metadata, **change)))
            before = {str(path): path.read_bytes() for path in cache_path.rglob("*") if path.is_file()}
            result = subprocess.run([str(self.root / "bin/cache"), "init"], cwd=self.root, env=self.env, capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertEqual(before, {str(path): path.read_bytes() for path in cache_path.rglob("*") if path.is_file()})

    def test_interrupted_initialization_and_unrecognized_directories(self):
        path = self.root / "data/owner/repo/cache"
        path.mkdir()
        (path / ".gitignore").write_text("*\n")
        self.assertEqual(json.loads(self.run_cli("cache", "init"))["status"], "initialized")
        (path / "cache.json").unlink()
        (path / "user-file").write_text("keep me")
        result = subprocess.run([str(self.root / "bin/cache"), "init"], cwd=self.root, env=self.env, capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertEqual((path / "user-file").read_text(), "keep me")

    def test_large_snapshot_reuses_objects_and_remains_offline(self):
        result = self.python(self.setup_code() + """
import copy, json, time
started = time.perf_counter()
template = manifest['items'][0]
manifest['items'] = []
for number in range(1, 2001):
    record = copy.deepcopy(template)
    record['identity'] = dict(kind='pr', number=number, database_id=number, node_id=None)
    manifest['items'].append(record)
manifest = seal_snapshot(manifest)
cache.publish(manifest, payloads)
assert len(cache.load(manifest['snapshot_id'])['items']) == 2000
objects = list(cache.path('objects').glob('*.json'))
print(json.dumps(dict(items=2000, objects=len(objects), elapsed_seconds=round(time.perf_counter() - started, 3))))
""")
        self.measurement = json.loads(result.stdout)
        self.assertEqual(self.measurement["objects"], 1)
        self.assertFalse((self.mock / "gh_calls.log").exists())

    def test_concurrent_publications_are_idempotent(self):
        self.python(self.setup_code())
        code = self.setup_code() + "cache.publish(manifest, payloads)\n"
        prelude = f"import sys; sys.path.insert(0, {str(self.root / 'bin')!r}); sys.path.append({str(ROOT / 'tests')!r})\n"
        workers = []
        try:
            for _ in range(4):
                workers.append(subprocess.Popen([sys.executable, "-c", prelude + code], cwd=self.root, env=dict(self.env, TRIAGE_ROOT=str(self.root)), stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True))

            for worker in workers:
                out, err = worker.communicate(timeout=20)
                self.assertEqual(worker.returncode, 0, out + err)
        finally:
            for worker in workers:
                if worker.poll() is None:
                    worker.kill()

                worker.communicate()

        self.assertEqual(len(json.loads(self.run_cli("cache", "status"))["snapshots"]), 1)
