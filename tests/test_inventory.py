"""Inventory provenance, offline reuse, and legacy sync isolation; fake GitHub only."""

import copy
import json
import subprocess
import sys


from support import InventoryFixture


class InventoryTests(InventoryFixture):
    def test_capture_json_guard_and_empty_outcome(self):
        result = self.fetch_inventory(None, "--full", "--json", "--expected-repo", "wrong/repo", ok=False)
        self.assertIn("expected-repo", result.stderr)
        self.assertEqual(self.calls(), [])
        self.assertFalse((self.root / "data/owner/repo/cache").exists())
        result = json.loads(self.fetch_inventory(None, "--full", "--json", "--expected-repo", "owner/repo").stdout)
        self.assertEqual(result["status"], "imported")
        self.assertEqual(result["scope"], "full")
        self.assertEqual(result["items"], 1)
        self.assertEqual(result["repository"]["full_name"], "owner/repo")
        self.assertEqual(len(result["snapshot_id"]), 64)
        result = json.loads(self.fetch_inventory([], "--full", "--json").stdout)
        self.assertEqual(result["status"], "empty")
        self.assertIsNone(result["snapshot_id"])
        self.assertEqual(result["items"], 0)
        self.assertNotIn("synced_through", self.meta())
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())


    def test_offline_reuse_saves_repeated_body_requests_without_claiming_detail(self):
        self.fetch_inventory()
        self.assertEqual(len(self.calls()), 2)
        for _ in range(2):
            result = self.run_cache("read", "pr", "discussion")
            self.assertEqual(result["data"]["summary"]["body"], "Body with evidence")
            self.assertEqual(result["problems"]["summary"], ["partial"])
            self.assertEqual(result["problems"]["comments"], ["missing"])
            self.assertIsNone(result["identity"]["database_id"])
            self.assertIsNone(result["revision"]["head_sha"])

        self.assertEqual(len(self.calls()), 2, "repeated local body reads avoid detail requests; discussion still missing")
        before = self.meta()
        snapshot = json.loads(self.cli("cache", "import-inventory").stdout)["snapshot_id"]
        self.assertEqual(snapshot, result["snapshot_id"])
        self.assertEqual(self.meta(), before)
        self.assertEqual(len(self.calls()), 2)
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())
        complete = self.run_cache("fetch", "pr", "discussion")
        self.assertFalse(any(complete["problems"].values()))
        self.assertEqual(complete["identity"]["database_id"], 101, "PR detail ID must not conflict with issue-list ID")
        fixed = self.run_cache("read", "pr", "discussion", "--snapshot", snapshot)
        self.assertEqual(fixed["problems"]["summary"], ["partial"])

    def test_sync_keeps_bodies_and_list_ids_out_of_ledger(self):
        self.fetch_inventory([self.row("issue"), self.row("pr", 2)])
        self.cli("sync")
        ledger = self.ledger()
        self.assertEqual(len(ledger), 2)
        for row in ledger.values():
            self.assertNotIn("body", row)
            self.assertNotIn("inventory_ids", row)
            self.assertFalse(row["reviewed"])

        self.assertEqual(self.meta()["synced_through"], self.meta()["fetched_at"])
        self.assertEqual(json.loads(self.cli("cache", "import-inventory").stdout)["items"], 2)

    def test_incremental_closed_pr_keeps_source_state_without_merge_claim(self):
        self.fetch_inventory()
        self.cli("sync")
        from datetime import datetime, timedelta
        since = (datetime.strptime(self.meta()["synced_through"], "%Y-%m-%dT%H:%M:%SZ") - timedelta(minutes=5)).strftime("%Y-%m-%dT%H:%M:%SZ")
        row = self.row()
        row["state"] = "closed"
        endpoint = f"repos/owner/repo/issues?state=all&since={since}&per_page=100&page=1"
        self.responses[endpoint] = dict(data=[row])
        self.fetch_inventory()
        result = self.run_cache("read", "pr", "discussion")
        data = result["data"]["summary"]
        self.assertEqual((data["source_state"], data["state"]), ("closed", "unknown"))
        self.assertEqual(data["inventory"]["mode"], "incremental")
        self.assertEqual(data["inventory"]["since"], since)

    def test_failed_pagination_or_budget_preserves_previous_raw(self):
        self.fetch_inventory()
        before = self.meta()
        first = "repos/owner/repo/issues?state=open&per_page=100&page=1"
        second = "repos/owner/repo/issues?state=open&per_page=100&page=2"
        self.responses[first] = dict(data=[self.row()], headers={"Link": '<ignored>; rel="next"'})
        self.responses[second] = dict(status=503)
        (self.mock / "responses.json").write_text(json.dumps(self.responses))
        self.cli("fetch", "--cache-inventory", "--full", ok=False)
        self.assertEqual(self.meta(), before)
        self.fetch_inventory(None, "--request-budget", "1", ok=False)
        self.assertEqual(self.meta(), before)

    def test_pagination_rejects_duplicates_and_preserves_scope(self):
        first = "repos/owner/repo/issues?state=open&per_page=100&page=1"
        second = "repos/owner/repo/issues?state=open&per_page=100&page=2"
        self.responses[first] = dict(data=[self.row()], headers={"Link": '<ignored>; rel="next"'})
        self.responses[second] = dict(data=[self.row("issue", 2)])
        (self.mock / "responses.json").write_text(json.dumps(self.responses))
        self.cli("fetch", "--cache-inventory")
        self.assertEqual(self.meta()["inventory"]["pages"], 2)
        before = self.meta()
        self.responses[second] = dict(data=[self.row()])
        (self.mock / "responses.json").write_text(json.dumps(self.responses))
        self.cli("fetch", "--cache-inventory", ok=False)
        self.assertEqual(self.meta(), before)

    def test_import_rejects_legacy_corrupt_foreign_and_future_metadata(self):
        self.fetch_inventory()
        original = self.meta()
        path = self.root / "data/owner/repo/raw/fetch_meta.json"
        for edit in (lambda m: m.pop("inventory"), lambda m: m.update(raw_sha256="0" * 64), lambda m: m["inventory"].update(schema_version=2), lambda m: m["inventory"]["repository"].update(host="other.example"), lambda m: m["inventory"].update(endpoint="arbitrary"), lambda m: m["inventory"].update(pagination_complete=False)):
            meta = copy.deepcopy(original)
            edit(meta)
            path.write_text(json.dumps(meta))
            self.cli("cache", "import-inventory", ok=False)

        self.assertEqual(len(self.calls()), 2)

    def test_invalid_source_rows_are_not_published(self):
        for change in (dict(body=3), dict(html_url="https://github.com/other/repo/pull/1"), dict(updated_at=None), dict(state="closed"), dict(id=None)):
            row = self.row()
            row.update(change)
            self.fetch_inventory([row], ok=False)
            self.assertFalse((self.root / "data/owner/repo/raw/fetch_meta.json").exists())

    def test_empty_inventory_does_not_infer_closure_or_create_empty_snapshot(self):
        self.fetch_inventory([])
        result = json.loads(self.cli("cache", "import-inventory").stdout)
        self.assertEqual(result["items"], 0)
        self.assertIsNone(result["snapshot_id"])
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())

    def test_throttle_blocks_next_inventory_before_network(self):
        endpoint = "repos/owner/repo/issues?state=open&per_page=100&page=1"
        self.responses[endpoint] = dict(status=429, headers={"Retry-After": "120"})
        (self.mock / "responses.json").write_text(json.dumps(self.responses))
        self.cli("fetch", "--cache-inventory", ok=False)
        count = len(self.calls())
        self.cli("fetch", "--cache-inventory", ok=False)
        self.assertEqual(len(self.calls()), count)
        self.assertFalse((self.root / "data/owner/repo/raw/fetch_meta.json").exists())

    def test_import_old_observation_does_not_replace_later_detail(self):
        self.fetch_inventory()
        detail = self.run_cache("fetch", "pr", "discussion")
        self.cli("cache", "import-inventory")
        self.assertEqual(self.run_cache("read", "pr", "discussion")["snapshot_id"], detail["snapshot_id"])

    def test_failed_cache_publication_can_import_raw_without_refetch(self):
        endpoint = "repos/owner/repo/issues?state=open&per_page=100&page=1"
        self.responses[endpoint] = dict(data=[self.row()])
        (self.mock / "responses.json").write_text(json.dumps(self.responses))
        code = """
import runpy, sys
sys.path.insert(0, 'bin')
from _cache import EvidenceCache
def fail(*args):
    raise OSError('injected cache publication failure')
EvidenceCache.publish = fail
sys.argv = ['bin/fetch', '--cache-inventory']
runpy.run_path('bin/fetch', run_name='__main__')
"""
        result = subprocess.run([sys.executable, "-c", code], cwd=self.root, env=self.env, capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("injected cache publication failure", result.stderr)
        meta = self.meta()
        count = len(self.calls())
        imported = json.loads(self.cli("cache", "import-inventory").stdout)
        self.assertEqual(imported["items"], 1)
        self.assertEqual(self.meta(), meta)
        self.assertEqual(len(self.calls()), count)
        self.cli("sync")
        self.assertIn(("pr", 1), self.ledger())
