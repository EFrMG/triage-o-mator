"""The optional offline retrieval bridge preserves literal search and evidence authority."""

import importlib.util
import json
import shutil
import sqlite3
import subprocess
import sys
import unittest
from pathlib import Path

from support import ROOT, Workspace, summary


class RepositionTests(Workspace):
    def setUp(self):
        super().setUp()
        shutil.copytree(ROOT / "vendor/reposition", self.root / "vendor/reposition")
        self.seed_pr()
        self.responses["repos/owner/repo/pulls/1"] = dict(data=summary(body="Terminal blank after suspend. Renderer reset, not a disconnected session socket."))
        self.snapshot = self.json_cli("cache", "fetch", "--kind", "pr", "--number", "1", "--profile", "discussion")["snapshot_id"]

    def offline(self, *args, no_site=False, expected=0, environment=None):
        result = subprocess.run([sys.executable, *(["-S"] if no_site else []), str(self.root / "bin/cache"), *args], cwd=self.root, env=environment or self.env, capture_output=True, text=True, timeout=30)
        self.assertEqual(result.returncode, expected, result.stderr + result.stdout)

        return result

    def test_literal_search_works_without_the_optional_package(self):
        calls = self.calls()
        self.assertIn("search-info", self.offline("--help", no_site=True).stdout)
        result = json.loads(self.offline("search", "--snapshot", self.snapshot, "--query", "Renderer", "--component", "summary", no_site=True).stdout)
        self.assertEqual(result["matched_items"], 1)
        missing = self.offline("query", "--snapshot", self.snapshot, "--query", "Renderer", no_site=True, expected=2)
        self.assertIn("audited Reposition is unavailable", missing.stderr)
        abbreviated_cache = self.offline("query", "--snapshot", self.snapshot, "--cach", str(self.root / "other-cache"), "--query", "Renderer", no_site=True, expected=2)
        self.assertIn("cache namespace is owned by this install", abbreviated_cache.stderr)

        unsupported = self.root / "unsupported/reposition"
        unsupported.mkdir(parents=True)
        (unsupported / "__init__.py").write_text('__version__ = "999.0"\n')
        (unsupported / "cli.py").write_text('def main(*args):\n    raise AssertionError("untested engine must not run")\n')
        environment = dict(self.env, PYTHONPATH=str(unsupported.parent))
        rejected = self.offline("query", "--snapshot", self.snapshot, "--query", "Renderer", no_site=True, expected=2, environment=environment)
        self.assertIn("audited Reposition is unavailable", rejected.stderr)

        broken = self.root / "broken/reposition"
        broken.mkdir(parents=True)
        (broken / "__init__.py").write_text("import missing_reposition_runtime_capability\n")
        environment = dict(self.env, PYTHONPATH=str(broken.parent))
        failed = self.offline("query", "--snapshot", self.snapshot, "--query", "Renderer", no_site=True, expected=2, environment=environment)
        self.assertIn("audited Reposition is unavailable", failed.stderr)
        fallback = json.loads(self.offline("search", "--snapshot", self.snapshot, "--query", "Renderer", "--component", "summary", no_site=True, environment=environment).stdout)
        self.assertEqual(fallback["matched_items"], 1)
        self.assertEqual(self.calls(), calls)

    def test_managed_index_storage_and_automatic_setup_fallback_are_visible_offline(self):
        calls = self.calls()
        before = self.json_cli("cache", "usage")
        sidecars = self.root / "data/owner/repo/reposition"
        sidecars.mkdir()
        (sidecars / "unused.sqlite").write_bytes(b"index" * 1000)
        after = self.json_cli("cache", "usage")
        self.assertGreater(after["allocated_categories"]["reposition"], 0)
        self.assertEqual(after["allocated_bytes"] - before["allocated_bytes"], after["allocated_categories"]["reposition"])

        probe = subprocess.run([sys.executable, "-c", """
import _cache
from _cache import CacheCapacityError, EvidenceCache
from _evidence import repository
from _reposition import bounded_index_arguments
cache = EvidenceCache(repository('owner/repo'))
used = cache.usage()['allocated_bytes']
_cache.DATASET_LIMIT_BYTES = used + _cache.DATASET_RESERVE_BYTES + 2_000_000
assert bounded_index_arguments(cache, [])[-1] == '2000000'
_cache.DATASET_LIMIT_BYTES = used + _cache.DATASET_RESERVE_BYTES + 65_535
try:
    bounded_index_arguments(cache, [])
except ValueError as exc:
    assert 'combined 5 GB' in str(exc)
else:
    raise AssertionError('index build exceeded the combined budget')
try:
    cache._check_capacity(65_536)
except CacheCapacityError:
    pass
else:
    raise AssertionError('evidence acquisition ignored managed indexes')
"""], cwd=self.root, env=dict(self.env, PYTHONPATH=str(self.root / "bin")), capture_output=True, text=True)
        self.assertEqual(probe.returncode, 0, probe.stderr)

        disabled = self.json_cli("reposition-env", "disable")
        self.assertEqual(disabled["state"], "off")
        self.assertEqual(self.json_cli("reposition-env", "status"), disabled)
        blocked = self.offline("query", "--snapshot", self.snapshot, "--query", "Renderer", expected=2)
        self.assertIn("Reposition is OFF", blocked.stderr)
        self.assertFalse(self.json_cli("cache", "usage")["reposition_environment"]["enabled"])

        python = self.root / ".reposition-venv/bin/python"
        python.parent.mkdir(parents=True)
        attempts = self.root / "attempts"
        python.write_text(f"#!/bin/sh\ncase \"$*\" in *'import sys, sqlite3'*|*'-m pip --version'*) exit 0 ;; esac\nprintf x >> '{attempts}'\nexit 1\n")
        python.chmod(0o755)
        failed = self.json_cli("reposition-env", "--retry", "ensure")
        self.assertTrue(failed["fallback"])
        self.assertEqual(failed["state"], "off")
        first_attempts = attempts.read_text()
        self.assertEqual(self.json_cli("reposition-env", "ensure"), failed)
        self.assertEqual(attempts.read_text(), first_attempts)
        native = self.json_cli("cache", "search", "--snapshot", self.snapshot, "--query", "Renderer", "--component", "summary")
        self.assertEqual(native["matched_items"], 1)

        shutil.rmtree(python.parent.parent)
        self.assertTrue(self.json_cli("reposition-env", "--retry", "ensure")["enabled"])
        self.assertEqual(self.json_cli("reposition-env", "status")["state"], "on")
        source = self.root / "vendor/reposition/0.2.0.dev1/reposition-source.tar.gz"
        original_source = source.read_bytes()
        source.write_bytes(original_source + b"tampered")
        self.assertEqual(self.json_cli("reposition-env", "status")["state"], "unavailable")
        source.write_bytes(original_source)
        wheel = next((self.root / "vendor/reposition").rglob("reposition-*.whl"))
        wheel.write_bytes(wheel.read_bytes() + b"tampered")
        self.assertEqual(self.json_cli("reposition-env", "status")["state"], "unavailable")
        self.assertEqual(self.json_cli("reposition-env", "--retry", "ensure")["state"], "off")
        self.assertEqual(self.calls(), calls)

    @unittest.skipUnless(importlib.util.find_spec("reposition"), "optional Reposition integration environment")
    def test_ranked_retrieval_is_identity_bound_and_does_not_change_evidence(self):
        from reposition.models import canonical, sha256

        self.assertTrue(self.json_cli("reposition-env", "enable")["enabled"])
        cache = self.root / "data/owner/repo/cache"
        before = {str(p.relative_to(cache)): p.read_bytes() for p in cache.rglob("*") if p.is_file()}
        calls = self.calls()
        database = self.root / "data/owner/repo/reposition/search.sqlite"
        self.offline("search-index", "--snapshot", self.snapshot, "--db", str(database))
        self.assertEqual((database.parent / ".gitignore").read_text(), "*\n")
        self.assertGreater(self.json_cli("cache", "usage")["allocated_categories"]["reposition"], 0)
        unmanaged = self.offline("search-index", "--snapshot", self.snapshot, "--db", str(self.root / "unmanaged.sqlite"), expected=2)
        self.assertIn("managed reposition directory", unmanaged.stderr)
        query_text = self.offline("query", "--snapshot", self.snapshot, "--db", str(database), "--query", '"terminal blank after suspend"', "--max-bytes", "6000").stdout
        query = json.loads(query_text)
        self.assertLessEqual(len(query_text.encode()), 6000)
        fragment = query["items"][0]["fragments"][0]
        retrieved = json.loads(self.offline("retrieve", "--snapshot", self.snapshot, "--db", str(database), "--unit", fragment["unit_id"], "--checkpoint", query["checkpoint"]).stdout)
        self.assertTrue(retrieved["items"][0]["fragments"][0]["verified"])
        info = json.loads(self.offline("search-info", "--snapshot", self.snapshot, "--db", str(database)).stdout)
        self.assertFalse(info["source_checkpoint_verified"])
        self.assertFalse(info["source_payloads_verified"])
        self.offline("search-info", "--snapshot", "0" * 64, "--db", str(database), expected=2)
        self.offline("query", "--cache", str(cache), "--snapshot", self.snapshot, "--query", "Renderer", expected=2)

        foreign = self.root / "foreign.sqlite"
        for changes in ({"full_name": "other/repo"}, {"host": "other.example"}, {"database_id": 999}, {"node_id": "R_other"}):
            with self.subTest(changes=changes):
                shutil.copyfile(database, foreign)
                with sqlite3.connect(foreign) as connection:
                    metadata = json.loads(connection.execute("SELECT data FROM manifest").fetchone()[0])
                    metadata.pop("checksum")
                    metadata["repository"].update(changes)
                    metadata["checksum"] = sha256(canonical(metadata))
                    connection.execute("UPDATE manifest SET data=?", (canonical(metadata),))

                rejected = self.offline("search-info", "--db", str(foreign), expected=2)
                self.assertIn("repository", rejected.stderr)

        after = {str(p.relative_to(cache)): p.read_bytes() for p in cache.rglob("*") if p.is_file()}
        self.assertEqual(after, before)
        self.assertEqual(self.calls(), calls)
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())

    def test_checkout_virtual_environment_is_used_without_activation(self):
        self.assertTrue(self.json_cli("reposition-env", "enable")["enabled"])
        python = self.root / ".reposition-venv/bin/python"
        site_packages = subprocess.check_output([str(python), "-c", "import sysconfig; print(sysconfig.get_paths()['purelib'])"], text=True).strip()
        package = Path(site_packages) / "reposition"
        self.assertEqual(self.offline("query", "--help").returncode, 0)
        shadow = self.root / "shadow/reposition"
        shadow.mkdir(parents=True)
        (shadow / "__init__.py").write_text('raise AssertionError("shadow package ran")\n')
        environment = dict(self.env, PYTHONPATH=str(shadow.parent))
        self.assertIn("usage:", self.offline("query", "--help", environment=environment).stdout)
        (package / "__init__.py").write_text('__version__ = "0.2.0.dev1"\n')

        result = self.offline("query", "--help", expected=2)
        self.assertIn("audited Reposition is unavailable", result.stderr)
        self.assertTrue(self.json_cli("reposition-env", "enable")["enabled"])
