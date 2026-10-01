"""The optional offline retrieval bridge preserves literal search and evidence authority."""

import importlib.util
import json
import shutil
import sqlite3
import subprocess
import sys
import unittest
import venv
from pathlib import Path

from support import Workspace, summary


class RepositionTests(Workspace):
    def setUp(self):
        super().setUp()
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
        self.assertIn("install Reposition 0.2.0.dev1", missing.stderr)
        abbreviated_cache = self.offline("query", "--snapshot", self.snapshot, "--cach", str(self.root / "other-cache"), "--query", "Renderer", no_site=True, expected=2)
        self.assertIn("cache namespace is owned by this install", abbreviated_cache.stderr)

        unsupported = self.root / "unsupported/reposition"
        unsupported.mkdir(parents=True)
        (unsupported / "__init__.py").write_text('__version__ = "999.0"\n')
        (unsupported / "cli.py").write_text('def main(*args):\n    raise AssertionError("untested engine must not run")\n')
        environment = dict(self.env, PYTHONPATH=str(unsupported.parent))
        rejected = self.offline("query", "--snapshot", self.snapshot, "--query", "Renderer", no_site=True, expected=2, environment=environment)
        self.assertIn("installed version is 999.0", rejected.stderr)

        broken = self.root / "broken/reposition"
        broken.mkdir(parents=True)
        (broken / "__init__.py").write_text("import missing_reposition_runtime_capability\n")
        environment = dict(self.env, PYTHONPATH=str(broken.parent))
        failed = self.offline("query", "--snapshot", self.snapshot, "--query", "Renderer", no_site=True, expected=2, environment=environment)
        self.assertIn("Reposition cannot load because Python module 'missing_reposition_runtime_capability' is unavailable", failed.stderr)
        fallback = json.loads(self.offline("search", "--snapshot", self.snapshot, "--query", "Renderer", "--component", "summary", no_site=True, environment=environment).stdout)
        self.assertEqual(fallback["matched_items"], 1)
        self.assertEqual(self.calls(), calls)

    @unittest.skipUnless(importlib.util.find_spec("reposition"), "optional Reposition integration environment")
    def test_ranked_retrieval_is_identity_bound_and_does_not_change_evidence(self):
        from reposition.models import canonical, sha256

        cache = self.root / "data/owner/repo/cache"
        before = {str(p.relative_to(cache)): p.read_bytes() for p in cache.rglob("*") if p.is_file()}
        calls = self.calls()
        database = self.root / "search.sqlite"
        self.offline("search-index", "--snapshot", self.snapshot, "--db", str(database))
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
        venv.create(self.root / ".reposition-venv", with_pip=False)
        python = self.root / ".reposition-venv/bin/python"
        site_packages = subprocess.check_output([str(python), "-c", "import sysconfig; print(sysconfig.get_paths()['purelib'])"], text=True).strip()
        package = Path(site_packages) / "reposition"
        package.mkdir()
        (package / "__init__.py").write_text('__version__ = "999.0"\n')
        (package / "cli.py").write_text("def main(argv):\n    raise AssertionError('untested engine must not run')\n")

        result = self.offline("query", "--help", expected=2)
        self.assertIn("installed version is 999.0", result.stderr)
