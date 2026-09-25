"""Offline artifact catalogs: bounded metadata windows, guarded membership and no implicit payload/network reads."""

import json
import subprocess
import sys
from datetime import datetime, timedelta


from support import CorpusFixture, InventoryFixture
from support import AcquisitionFixture


class CatalogTests(AcquisitionFixture):
    row = InventoryFixture.row
    cli = InventoryFixture.cli
    fetch_inventory = InventoryFixture.fetch_inventory
    create = CorpusFixture.create

    def discover(self, kind="inventories", *args):
        return json.loads(self.cli("cache", "discover", "--kind", kind, *args).stdout)

    def test_absent_catalog_is_empty_offline_without_initialization(self):
        for kind in ("inventories", "corpora"):
            result = self.discover(kind)
            self.assertEqual(result["items"], [])
            self.assertEqual(result["requests"], 0)
            self.assertEqual(result["cache_status"], "not-initialized")
            self.assertIsNone(result["continuation"])

        self.assertEqual(self.calls(), [])
        self.assertFalse((self.root / "data/owner/repo/cache").exists())

    def test_inventory_candidates_and_corpora_never_read_payloads(self):
        identifier, inventory = self.create()
        before = len(self.calls())
        script = """
import sys, json
sys.path.insert(0, 'bin')
from _cache import EvidenceCache
from _catalog import discover
from _evidence import repository
from unittest.mock import patch
cache = EvidenceCache(repository('owner/repo'))
with patch.object(cache, 'read_object', side_effect=AssertionError('payload read')):
    print(json.dumps([discover(cache, kind) for kind in ('inventories', 'corpora')]))
"""
        result = subprocess.run([sys.executable, "-c", script], cwd=self.root, env=self.env, capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)
        inventories, corpora = json.loads(result.stdout)
        self.assertEqual(inventories["items"][0]["id"], inventory)
        self.assertEqual(inventories["items"][0]["prs"], 2)
        self.assertEqual(corpora["items"][0]["id"], identifier)
        self.assertNotIn("Body with evidence", result.stdout)
        self.assertNotIn("last_run", result.stdout)
        self.assertEqual(len(self.calls()), before)

    def test_continuation_is_bound_to_catalog_membership_not_mutable_progress(self):
        identifier, inventory = self.create()
        for profile in ("pr-context", "pr-code"):
            self.cli("cache", "corpus-create", "--snapshot", inventory, "--profile", profile)

        first = self.discover("corpora", "--limit", "1")
        self.assertEqual(first["pagination"]["total"], 3)
        self.cli("cache", "corpus-run", identifier, "--request-budget", "1", "--compact")
        second = self.discover("corpora", "--limit", "1", "--offset", "1", "--checkpoint", first["checkpoint"])
        self.assertNotEqual(first["items"][0]["id"], second["items"][0]["id"])
        self.cli("cache", "corpus-create", "--snapshot", inventory, "--profile", "pr-comparison")
        result = self.cli("cache", "discover", "--kind", "corpora", "--offset", "1", "--checkpoint", first["checkpoint"], ok=False)
        self.assertIn("membership changed", result.stderr)
        for args in (("--offset", "1"), ("--limit", "101"), ("--limit", "0"), ("--offset", "-1"), ("--offset", "50", "--checkpoint", self.discover("corpora")["checkpoint"]), ("--checkpoint", "wrong")):
            self.cli("cache", "discover", "--kind", "corpora", *args, ok=False)

    def test_detail_pages_can_be_empty_without_hiding_continuation(self):
        _, inventory = self.create()
        self.run_cache("fetch", "pr", "discussion")
        all_ids = sorted(path.stem for path in (self.root / "data/owner/repo/cache/snapshots").glob("*.json"))
        token = self.discover()["checkpoint"]
        candidates = []
        empty = 0
        before = len(self.calls())
        for offset in range(len(all_ids)):
            result = self.discover("inventories", "--limit", "1", "--offset", str(offset), "--checkpoint", token)
            candidates.extend(row["id"] for row in result["items"])
            empty += not result["items"]
            self.assertEqual(result["pagination"]["returned"], 1)
            self.assertEqual(result["excluded"], int(not result["items"]))
            self.assertEqual(result["pagination"]["next_offset"], offset + 1 if offset + 1 < len(all_ids) else None)

        self.assertEqual(candidates, [inventory])
        self.assertGreater(empty, 0)
        self.assertEqual(len(self.calls()), before)

    def test_corrupt_payload_remains_candidate_but_creation_refuses_it(self):
        _, inventory = self.create()
        manifest = json.loads(self.cli("cache", "show", inventory).stdout)
        sha = manifest["items"][0]["components"]["summary"]["object"]["sha256"]
        next((self.root / "data/owner/repo/cache/objects").glob(sha + "*")).write_text("broken")
        self.assertTrue(self.discover()["items"][0]["selectable"])
        self.cli("cache", "corpus-create", "--snapshot", inventory, ok=False)

    def test_incremental_inventory_is_excluded(self):
        _, inventory = self.create()
        self.cli("sync")
        since = (datetime.strptime(self.meta()["synced_through"], "%Y-%m-%dT%H:%M:%SZ") - timedelta(minutes=5)).strftime("%Y-%m-%dT%H:%M:%SZ")
        self.responses[f"repos/owner/repo/issues?state=all&since={since}&per_page=100&page=1"] = dict(data=[self.row()])
        (self.mock / "responses.json").write_text(json.dumps(self.responses))
        self.cli("fetch", "--cache-inventory")
        before = len(self.calls())
        result = self.discover()
        self.assertEqual([row["id"] for row in result["items"]], [inventory])
        self.assertEqual(result["excluded"], 1)
        self.assertEqual(len(self.calls()), before)

    def test_corrupt_future_and_symlink_rows_are_visible_but_disabled(self):
        identifier, inventory = self.create()
        path = self.root / "data/owner/repo/cache/snapshots" / (inventory + ".json")
        original = path.read_text()
        for value in ("not JSON", json.dumps(dict(json.loads(original), schema_version=999))):
            path.write_text(value)
            row = self.discover()["items"][0]
            self.assertFalse(row["selectable"])
            self.assertEqual(row["classification"], "unavailable")

        path.unlink()
        path.symlink_to(self.root / "config/repo")
        self.assertFalse(self.discover()["items"][0]["selectable"])
        self.assertFalse(self.discover("corpora")["items"][0]["selectable"])
        self.assertEqual((self.root / "config/repo").read_text().strip(), "owner/repo")

    def test_membership_change_during_page_is_refused(self):
        self.create()
        script = """
import sys
sys.path.insert(0, 'bin')
import _catalog
from _cache import EvidenceCache
from _evidence import repository
original = _catalog.entry
def changed(cache, kind, identifier):
    result = original(cache, kind, identifier)
    cache.path('snapshots', 'f'*64 + '.json').write_text('{}')
    return result
_catalog.entry = changed
try:
    _catalog.discover(EvidenceCache(repository('owner/repo')), 'inventories')
except ValueError as error:
    assert 'changed during discovery' in str(error)
else:
    raise AssertionError('accepted mixed catalog')
"""
        result = subprocess.run([sys.executable, "-c", script], cwd=self.root, env=self.env, capture_output=True, text=True, timeout=10)
        self.assertEqual(result.returncode, 0, result.stderr)
