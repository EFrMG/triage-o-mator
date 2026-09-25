"""Disposable index recovery, concurrency, and bounded historical I/O; all evidence is synthetic."""

import json
import subprocess
import sys


from support import EvidenceFixture
from support import CheckoutTest, ROOT


class CurrentIndexTests(CheckoutTest):
    python = EvidenceFixture.python
    setup_code = EvidenceFixture.setup_code

    def test_latest_partial_out_of_order_and_ties_preserve_fixed_reads(self):
        self.python(self.setup_code() + """
import copy
from unittest.mock import patch
cache.publish(manifest, payloads)
partial = copy.deepcopy(manifest)
partial['completed_at'] = '2026-09-21T12:03:00Z'
partial['items'][0]['components']['files'].update(status='partial', error='interrupted', pagination_complete=False)
partial = seal_snapshot(partial)
cache.publish(partial, {})
assert cache.latest('pr', 1)[0] == partial
cache.publish(manifest, {})
assert cache.latest('pr', 1)[0] == partial
tie = seal_snapshot(dict(partial, started_at='2026-09-21T12:01:00Z'))
cache.publish(tie, {})
assert cache.latest('pr', 1)[0] == tie
other = copy.deepcopy(tie)
other['items'][0]['components']['files']['error'] = 'different gap'
other = seal_snapshot(other)
cache.publish(other, {})
assert cache.latest('pr', 1)[0]['snapshot_id'] == max(tie['snapshot_id'], other['snapshot_id'])
assert cache.latest('issue', 1) is None
assert cache.latest('pr', 999) is None
with patch('pathlib.Path.glob', side_effect=AssertionError('fixed read searched history')):
    assert cache.load(manifest['snapshot_id']) == manifest
""")

    def test_missing_index_rebuilds_offline_and_empty_unbound_index_can_bind(self):
        self.run_cli("cache", "init")
        self.python("""
from _cache import EvidenceCache
from _evidence import repository
assert EvidenceCache(repository('owner/repo')).latest('pr', 1) is None
""")
        self.python(self.setup_code() + """
cache.publish(manifest, payloads)
cache.path('indexes', 'current.json').unlink()
assert cache.latest('pr', 1)[0] == manifest
""")
        result = json.loads(self.run_cli("cache", "rebuild-index"))
        self.assertEqual((result["items"], result["snapshots"]), (1, 1))
        self.assertFalse((self.mock / "gh_calls.log").exists())

    def test_corrupt_index_requires_explicit_rebuild_and_future_or_foreign_refused(self):
        self.python(self.setup_code() + """
import json
from _evidence import canonical, digest
cache.publish(manifest, payloads)
path = cache.path('indexes', 'current.json')
original = json.loads(path.read_text())
for invalid in ('{broken', json.dumps(dict(original, items={})), json.dumps(dict(original, checksum='0' * 64))):
    path.write_text(invalid)
    try:
        cache.latest('pr', 1)
    except ValueError as error:
        assert 'rebuild-index' in str(error)
    else:
        raise AssertionError('invalid index accepted')
    assert cache.load(manifest['snapshot_id']) == manifest
    cache.rebuild_index()
    assert cache.latest('pr', 1)[0] == manifest
for changed in (dict(schema_version=2), dict(repository=repository('owner/repo', host='foreign.example')), dict(repository=repository('owner/repo', database_id=999))):
    value = dict(original, **changed)
    value['checksum'] = digest(canonical({k: v for k, v in value.items() if k != 'checksum'}))
    path.write_text(json.dumps(value))
    before = path.read_bytes()
    for operation in (lambda: cache.latest('pr', 1), cache.rebuild_index):
        try:
            operation()
        except ValueError:
            pass
        else:
            raise AssertionError('incompatible index accepted')
        assert path.read_bytes() == before
""")

    def test_semantically_invalid_index_with_valid_checksum_is_rejected(self):
        self.python(self.setup_code() + """
import copy, json
from _evidence import canonical, digest
cache.publish(manifest, payloads)
path = cache.path('indexes', 'current.json')
original = json.loads(path.read_text())
for field, value in (('snapshot_id', '../escape'), ('identity', dict(kind='pr', number=2, database_id=123, node_id='PR_example')), ('completed_at', '2020-01-01T00:00:00Z')):
    changed = copy.deepcopy(original)
    changed['items']['pr:1'][field] = value
    changed['checksum'] = digest(canonical({k: v for k, v in changed.items() if k != 'checksum'}))
    path.write_text(json.dumps(changed))
    try:
        cache.latest('pr', 1)
    except ValueError:
        pass
    else:
        raise AssertionError('invalid index contract accepted')
    cache.rebuild_index()
""")

    def test_history_add_remove_and_directory_replacement_invalidate(self):
        self.python(self.setup_code() + """
import shutil
from _evidence import canonical
cache.publish(manifest, payloads)
second = seal_snapshot(dict(manifest, completed_at='2026-09-21T12:04:00Z'))
path = cache.path('snapshots', second['snapshot_id'] + '.json')
path.write_text(canonical(second))
assert cache.latest('pr', 1)[0] == second
path.unlink()
assert cache.latest('pr', 1)[0] == manifest
cache.path('snapshots').rename(cache.path('old-snapshots'))
shutil.copytree(cache.path('old-snapshots'), cache.path('snapshots'))
assert cache.latest('pr', 1)[0] == manifest
""")

    def test_selected_corruption_never_falls_back_and_rebuild_audits_older_objects(self):
        self.python(self.setup_code() + """
import copy
from _evidence import artifact_ref, object_name
cache.publish(manifest, payloads)
second = copy.deepcopy(manifest)
second['completed_at'] = '2026-09-21T12:04:00Z'
payload = '[{"path":"new.py","status":"modified"}]'
ref = artifact_ref(payload)
second['items'][0]['components']['files']['object'] = ref
second = seal_snapshot(second)
cache.publish(second, {object_name(ref): payload})
oldref = manifest['items'][0]['components']['files']['object']
cache.object_path(oldref).write_text('corrupt historical object')
assert cache.latest('pr', 1)[0] == second
before = cache.path('indexes', 'current.json').read_bytes()
try:
    cache.rebuild_index()
except ValueError:
    pass
else:
    raise AssertionError('rebuild accepted corrupt history')
assert cache.path('indexes', 'current.json').read_bytes() == before
cache.object_path(ref).write_text('corrupt selected object')
try:
    cache.latest('pr', 1)
except ValueError:
    pass
else:
    raise AssertionError('selected corruption was hidden')
""")

    def test_stable_identity_conflicts_rejected_before_publication_and_during_rebuild(self):
        self.python(self.setup_code() + """
import copy
from _evidence import canonical
cache.publish(manifest, payloads)
original = cache.path('indexes', 'current.json').read_bytes()
for changes in (dict(database_id=999), dict(node_id='PR_other'), dict(number=2), dict(kind='issue')):
    changed = copy.deepcopy(manifest)
    changed['completed_at'] = '2026-09-21T12:04:00Z'
    changed['items'][0]['identity'].update(changes)
    if changes.get('kind') == 'issue':
        changed['items'][0]['components']['files'].update(status='not_applicable', source=None, fetched_at=None, object=None, expected_count=None, received_count=None, pagination_complete=None)
    changed = seal_snapshot(changed)
    try:
        cache.publish(changed, {})
    except ValueError:
        pass
    else:
        raise AssertionError('conflicting history published')
    assert not cache.path('snapshots', changed['snapshot_id'] + '.json').exists()
    assert cache.path('indexes', 'current.json').read_bytes() == original
    path = cache.path('snapshots', changed['snapshot_id'] + '.json')
    path.write_text(canonical(changed))
    try:
        cache.rebuild_index()
    except ValueError:
        pass
    else:
        raise AssertionError('conflicting history indexed')
    path.unlink()
    cache.rebuild_index()
    original = cache.path('indexes', 'current.json').read_bytes()
""")

    def test_symlinked_index_artifacts_are_refused(self):
        self.python(self.setup_code() + """
cache.publish(manifest, payloads)
outside = cache.root.parent / 'outside'
outside.write_text('do not modify')
for name in ('current.json', 'current.pending'):
    path = cache.path('indexes', name)
    if path.exists():
        path.unlink()
    path.symlink_to(outside)
    try:
        cache.rebuild_index()
    except ValueError:
        pass
    else:
        raise AssertionError('index symlink accepted')
    assert outside.read_text() == 'do not modify'
    path.unlink()
    cache.rebuild_index()
""")

    def test_identity_discovery_is_preserved_across_out_of_order_history(self):
        self.python(self.setup_code() + """
import copy, json
unknown = copy.deepcopy(manifest)
unknown['items'][0]['identity'].update(database_id=None, node_id=None)
unknown = seal_snapshot(unknown)
known = seal_snapshot(dict(manifest, completed_at='2026-09-21T12:03:00Z'))
cache.publish(known, payloads)
cache.publish(unknown, {})
assert cache.latest('pr', 1)[0] == known
cache.rebuild_index()
assert cache.latest('pr', 1)[0] == known
index = json.loads(cache.path('indexes', 'current.json').read_text())
assert index['items']['pr:1']['identity'] == known['items'][0]['identity']
assert cache.load(unknown['snapshot_id'])['items'][0]['identity']['node_id'] is None
""")

    def test_post_replace_directory_flush_failures_remain_recoverable(self):
        self.python(self.setup_code() + """
from unittest.mock import patch
import _storage
cache.publish(manifest, payloads)
original = _storage.sync_directory
for step, boundary in enumerate(('invalidation', 'snapshot', 'index'), 1):
    changed = seal_snapshot(dict(manifest, completed_at=f'2026-09-21T12:{step + 4:02d}:00Z'))
    previous = cache.latest('pr', 1)[0]
    index_syncs = 0
    def failing(directory):
        global index_syncs
        if directory.name == 'indexes':
            index_syncs += 1
            if (boundary == 'invalidation' and index_syncs == 1) or (boundary == 'index' and index_syncs == 2):
                raise OSError('injected directory flush failure')
        if boundary == 'snapshot' and directory.name == 'snapshots':
            raise OSError('injected snapshot flush failure')
        original(directory)
    try:
        with patch.object(_storage, 'sync_directory', failing):
            cache.publish(changed, {})
    except OSError:
        pass
    else:
        raise AssertionError('flush failure was not injected')
    assert cache.latest('pr', 1)[0] == (previous if boundary == 'invalidation' else changed)
    cache.publish(changed, {})
    assert cache.latest('pr', 1)[0] == changed
""")

    def test_failure_at_each_publication_boundary_recovers_latest_observation(self):
        self.python(self.setup_code() + """
from unittest.mock import patch
import _storage
cache.publish(manifest, payloads)
original_replace = _storage.os.replace
for step, boundary in enumerate(('current.pending', 'manifest-before', 'manifest-after', 'current.json', 'index-after'), 1):
    next_manifest = seal_snapshot(dict(manifest, completed_at=f'2026-09-21T12:{step + 4:02d}:00Z'))
    previous = cache.latest('pr', 1)[0]
    def replace(source, target):
        if target.name == boundary or (target.parent.name == 'snapshots' and boundary == 'manifest-before'):
            raise OSError('injected pre-replace interruption')
        original_replace(source, target)
        if (target.parent.name == 'snapshots' and boundary == 'manifest-after') or (target.name == 'current.json' and boundary == 'index-after'):
            raise OSError('injected post-replace interruption')
    try:
        with patch.object(_storage.os, 'replace', replace):
            cache.publish(next_manifest, {})
    except OSError:
        pass
    else:
        raise AssertionError('failure was not injected')
    expected = previous if boundary in ('current.pending', 'manifest-before') else next_manifest
    assert cache.latest('pr', 1)[0] == expected, boundary
    assert not cache.path('indexes', 'current.pending').exists()
    cache.publish(next_manifest, {})
    assert cache.latest('pr', 1)[0] == next_manifest
""")

    def test_process_death_after_manifest_cannot_hide_newer_partial(self):
        self.python(self.setup_code() + "cache.publish(manifest, payloads)\n")
        result = self.python(self.setup_code() + """
import os
from unittest.mock import patch
import _storage
manifest['completed_at'] = '2026-09-21T12:04:00Z'
manifest['items'][0]['components']['files'].update(status='partial', error='stopped', pagination_complete=False)
manifest = seal_snapshot(manifest)
original = _storage.os.replace
def killed(source, target):
    original(source, target)
    if target.parent.name == 'snapshots':
        os._exit(73)
with patch.object(_storage.os, 'replace', killed):
    cache.publish(manifest, {})
""", ok=False)
        self.assertEqual(result.returncode, 73)
        self.python(self.setup_code() + """
assert cache.path('indexes', 'current.pending').exists()
assert cache.latest('pr', 1)[1]['components']['files']['status'] == 'partial'
assert not cache.path('indexes', 'current.pending').exists()
""")

    def test_concurrent_publish_read_and_rebuild_keep_all_items(self):
        self.python(self.setup_code() + "cache.publish(manifest, payloads)\n")
        prelude = f"import sys; sys.path.insert(0, {str(self.root / 'bin')!r}); sys.path.append({str(ROOT / 'tests')!r})\n"
        scripts = [self.setup_code() + f"""
for number in range({start}, {start + 10}):
    manifest['items'][0]['identity'] = dict(kind='pr', number=number, database_id=number + 1000, node_id=f'PR_{{number}}')
    manifest = seal_snapshot(manifest)
    cache.publish(manifest, payloads)
    assert cache.latest('pr', number)[0] == manifest
""" for start in (2, 12)]
        scripts.append(self.setup_code() + "for _ in range(12):\n    cache.rebuild_index()\n    assert cache.latest('pr', 1)[0] == manifest\n")
        workers = []
        try:
            for code in scripts:
                workers.append(subprocess.Popen([sys.executable, "-c", prelude + code], cwd=self.root, env=dict(self.env, TRIAGE_ROOT=str(self.root)), stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True))

            for worker in workers:
                out, err = worker.communicate(timeout=20)
                self.assertEqual(worker.returncode, 0, out + err)
        finally:
            for worker in workers:
                if worker.poll() is None:
                    worker.kill()
                worker.communicate()

        result = json.loads(self.run_cli("cache", "rebuild-index"))
        self.assertEqual((result["items"], result["snapshots"]), (21, 21))
