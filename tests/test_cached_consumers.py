"""Cached similarity and group packets with fake GitHub and disposable installs only."""

import copy
import json
import subprocess
import sys



from support import AcquisitionFixture, comment, item, summary
from support import DIFF, PATCH


class CachedConsumerTests(AcquisitionFixture):
    def setUp(self):
        super().setUp()
        self.ledger_path = self.root / "data/owner/repo/ledger.jsonl"
        self.rows = [dict(item(n, "Fix lid suspend handling", kind="pr"), category="", reviewed=False) for n in (1, 2)]
        self.ledger_path.write_text("".join(json.dumps(r) + "\n" for r in self.rows))
        self.original_ledger = self.ledger_path.read_bytes()
        self.responses["repos/owner/repo/pulls/1/files?per_page=100&page=1"]["data"][0].update(patch=PATCH, additions=2, deletions=1)
        self.responses["repos/owner/repo/pulls/1#diff"] = dict(raw=DIFF)
        for key, value in list(self.responses.items()):
            if "/1" in key:
                self.responses[key.replace("/1", "/2")] = copy.deepcopy(value)

        self.responses["repos/owner/repo/pulls/2"] = dict(data=summary(number=2, id=102, node_id="PR_2", html_url="https://github.com/owner/repo/pull/2", title="Second PR", body="Second body"))
        self.responses["repos/owner/repo/issues/2/comments?per_page=100&page=1"] = dict(data=[comment(2)])
        self.save_responses()
        self.group = json.loads(self.run_cli("group", "create", "--title", "Suspend alternatives", "--by", "tester"))
        for n in (1, 2):
            self.group = json.loads(self.run_cli("group", "add", self.group["id"], "--kind", "pr", "--number", str(n), "--by", "tester"))

        self.group_path = self.root / f"data/owner/repo/groups/{self.group['id']}.json"
        self.original_group = self.group_path.read_bytes()

    def save_responses(self):
        (self.mock / "responses.json").write_text(json.dumps(self.responses))

    def cli(self, command, *args, ok=True):
        result = subprocess.run([str(self.root / "bin" / command), *args], cwd=self.root, env=self.env, capture_output=True, text=True, timeout=20)
        self.assertEqual(result.returncode == 0, ok, result.stderr)
        return result

    def similar(self, *args, ok=True):
        return self.cli("similar", "--kind", "pr", "--number", "1", *args, ok=ok)

    def export(self, *args, ok=True):
        return self.cli("group", "export", self.group["id"], *args, ok=ok)

    def prefetch(self):
        return [json.loads(self.cli("cache", "fetch", "--kind", "pr", "--number", str(n), "--profile", "pr-code").stdout) for n in (1, 2)]

    def script(self, code, ok=True):
        result = subprocess.run([sys.executable, "-c", "import sys; sys.path.insert(0, 'bin')\n" + code], cwd=self.root, env=self.env, capture_output=True, text=True, timeout=20)
        self.assertEqual(result.returncode == 0, ok, result.stderr)
        return result

    def test_offline_reuse_pins_every_member_and_preserves_decisions(self):
        snapshots = self.prefetch()
        calls = self.calls()
        result = json.loads(self.similar("--diff", "--cache-mode", "offline").stdout)
        self.assertEqual(result["diff_text"], DIFF)
        self.assertEqual(result["candidates"][0]["diff_text"], DIFF)
        self.assertEqual(result["evidence"]["snapshot_id"], snapshots[0]["snapshot_id"])
        self.assertEqual(result["candidates"][0]["evidence"]["snapshot_id"], snapshots[1]["snapshot_id"])
        self.assertEqual(result["candidates"][0]["score"], 1)

        output = self.export("--diff", "--cache-mode", "offline", "--format", "json")
        packet = json.loads(output.stdout)
        self.assertIn("offline evidence 1/2", output.stderr)
        self.assertEqual(packet["group"]["revision"], self.group["revision"])
        self.assertEqual([r["evidence"]["snapshot_id"] for r in packet["items"]], [r["snapshot_id"] for r in snapshots])
        self.assertTrue(all(not r["reviewed"] for r in packet["items"]))
        self.assertEqual(self.calls(), calls)
        self.assertEqual(self.ledger_path.read_bytes(), self.original_ledger)
        self.assertEqual(self.group_path.read_bytes(), self.original_group)

    def test_cache_preferred_reuses_and_refresh_reacquires_each_item(self):
        self.prefetch()
        count = len(self.calls())
        self.similar("--enrich", "--cache-mode", "cache-preferred")
        self.export("--diff", "--cache-mode", "cache-preferred")
        self.assertEqual(len(self.calls()), count)
        self.responses["repos/owner/repo/pulls/1"]["data"]["body"] = "New observation"
        self.save_responses()
        result = json.loads(self.similar("--enrich", "--cache-mode", "refresh").stdout)
        self.assertEqual(result["body"], "New observation")
        self.assertGreater(len(self.calls()), count)

    def test_offline_missing_and_partial_evidence_never_means_clean(self):
        result = json.loads(self.similar("--diff", "--cache-mode", "offline").stdout)
        self.assertEqual(result["evidence"]["problems"]["summary"], ["missing"])
        self.assertNotIn("diff_text", result)
        markdown = self.export("--diff", "--cache-mode", "offline").stdout
        self.assertIn("Snapshot: (none)", markdown)
        self.assertIn("body unavailable", markdown)
        self.assertIn("not a verified empty discussion", markdown)
        self.assertIn("diff: missing", markdown)
        self.assertNotIn("(no comments)", markdown)
        self.assertEqual(self.calls(), [])

        self.responses["repos/owner/repo/issues/1/comments?per_page=100&page=1"] = dict(status=503)
        self.responses["repos/owner/repo/pulls/1#diff"] = dict(raw=DIFF[:-6])
        self.save_responses()
        self.prefetch()
        count = len(self.calls())
        markdown = self.export("--diff", "--cache-mode", "offline", "--max-age", "0").stdout
        self.assertIn("Details: GitHub read returned HTTP 503", markdown)
        self.assertIn("partial", markdown)
        self.assertIn("outside freshness window", markdown)
        self.assertIn("not a verified empty discussion", markdown)
        self.assertEqual(len(self.calls()), count)

    def test_fixed_multi_item_snapshot_survives_later_refresh(self):
        first = self.prefetch()
        result = self.script(f"""
from _cache import EvidenceCache
from _evidence import repository, seal_snapshot
c = EvidenceCache(repository('owner/repo'))
manifests = [c.load(s) for s in { [r['snapshot_id'] for r in first]!r}]
m = dict(manifests[0])
m['completed_at'] = manifests[-1]['completed_at']
m['items'] = [manifest['items'][0] for manifest in manifests]
m.pop('snapshot_id')
m = seal_snapshot(m)
c.publish(m, {{}})
print(m['snapshot_id'])
""")
        snapshot = result.stdout.strip()
        self.responses["repos/owner/repo/pulls/1"]["data"]["body"] = "Later content"
        self.save_responses()
        self.export("--enrich", "--cache-mode", "refresh")
        count = len(self.calls())
        result = json.loads(self.similar("--diff", "--cache-mode", "offline", "--snapshot", snapshot).stdout)
        self.assertEqual(result["body"], "Body with evidence")
        self.assertEqual(result["candidates"][0]["body"], "Second body")
        md = self.export("--diff", "--cache-mode", "offline", "--snapshot", snapshot).stdout
        self.assertIn(snapshot, md)
        self.assertNotIn("Later content", md)
        self.assertEqual(len(self.calls()), count)

    def test_fixed_snapshot_missing_candidate_refuses_without_output(self):
        snapshot = self.prefetch()[0]["snapshot_id"]
        count = len(self.calls())
        out = self.root / "packet.json"
        out.write_text("Previous packet\n")
        result = self.export("--enrich", "--cache-mode", "offline", "--snapshot", snapshot, "--output", str(out), ok=False)
        self.assertIn("pr:2 is not in snapshot", result.stderr)
        self.assertEqual(out.read_text(), "Previous packet\n")
        result = self.similar("--enrich", "--cache-mode", "offline", "--snapshot", snapshot, ok=False)
        self.assertEqual(result.stdout, "")
        self.assertIn("pr:2 is not in snapshot", result.stderr)
        self.assertEqual(len(self.calls()), count)

    def test_rate_limit_stops_before_next_item_and_retains_old_export(self):
        self.responses["repos/owner/repo/issues/1/comments?per_page=100&page=1"] = dict(status=429)
        self.save_responses()
        path = self.root / "packet.md"
        path.write_text("Previous packet")
        result = self.export("--enrich", "--cache-mode", "refresh", "--output", str(path), ok=False)
        self.assertIn("enrichment stopped", result.stderr)
        self.assertEqual(path.read_text(), "Previous packet")
        self.assertFalse(any("pulls/2" in call[-1] for call in self.calls()))
        result = self.similar("--enrich", "--cache-mode", "refresh", ok=False)
        self.assertEqual(result.stdout, "")
        self.assertFalse(any("pulls/2" in call[-1] for call in self.calls()))

    def test_invalid_modes_fail_before_network_or_publication(self):
        for options in [("--cache-mode", "offline"), ("--enrich", "--snapshot", "0" * 64), ("--enrich", "--host", "example.test"), ("--enrich", "--cache-mode", "offline", "--request-budget", "0")]:
            with self.subTest(options=options):
                self.similar(*options, ok=False)
                self.export(*options, ok=False)

        self.cli("similar", "--pairs", "--enrich", "--cache-mode", "offline", ok=False)
        self.cli("similar", "--query", "lid", "--diff", ok=False)
        self.assertEqual(self.calls(), [])

    def test_explicit_host_links_and_missing_ledger_member(self):
        self.ledger_path.write_text(json.dumps(self.rows[0]) + "\n")
        packet = json.loads(self.export("--enrich", "--cache-mode", "offline", "--host", "example.test", "--format", "json").stdout)
        self.assertTrue(packet["items"][1]["missing_from_ledger"])
        self.assertEqual(packet["items"][1]["evidence"]["repository"]["host"], "example.test")
        md = self.export("--enrich", "--cache-mode", "offline", "--host", "example.test").stdout
        self.assertIn("https://example.test/owner/repo/pull/2", md)
        self.assertEqual(self.calls(), [])

    def test_export_is_frozen_even_when_cache_is_removed(self):
        self.prefetch()
        output = self.root / "exports/review.json"
        self.export("--diff", "--cache-mode", "offline", "--format", "json", "--output", str(output))
        saved = output.read_bytes()
        self.responses["repos/owner/repo/pulls/1"]["data"]["body"] = "Later content"
        self.save_responses()
        self.similar("--enrich", "--cache-mode", "refresh")
        cache = self.root / "data/owner/repo/cache"
        cache.rename(cache.with_name("cache.saved"))
        self.assertEqual(output.read_bytes(), saved)
        self.assertEqual(json.loads(saved)["items"][0]["diff_text"], DIFF)

    def test_killed_export_leaves_previous_complete_file(self):
        path = self.root / "packet.json"
        path.write_text("Previous packet\n")
        self.script(f"""
import os, runpy
from unittest.mock import patch
original = os.replace
def killed(source, destination):
    if str(destination) == {str(path)!r}:
        os._exit(77)
    return original(source, destination)
sys.argv = ['group', 'export', {self.group['id']!r}, '--format', 'json', '--output', {str(path)!r}]
with patch('os.replace', side_effect=killed):
    runpy.run_path('bin/group', run_name='__main__')
""", ok=False)
        self.assertEqual(path.read_text(), "Previous packet\n")
        self.export("--format", "json", "--output", str(path))
        self.assertEqual(json.loads(path.read_text())["group"]["id"], self.group["id"])
        self.assertEqual(self.calls(), [])

    def test_output_symlink_is_refused_without_touching_target(self):
        target = self.root / "keep.txt"
        target.write_text("Keep this")
        output = self.root / "packet.md"
        output.symlink_to(target)
        self.export("--output", str(output), ok=False)
        self.assertTrue(output.is_symlink())
        self.assertEqual(target.read_text(), "Keep this")

    def test_post_replace_flush_failure_leaves_complete_new_export(self):
        path = self.root / "packet.json"
        path.write_text("Previous packet")
        self.script(f"""
import runpy
from unittest.mock import patch
sys.argv = ['group', 'export', {self.group['id']!r}, '--format', 'json', '--output', {str(path)!r}]
with patch('_storage.sync_directory', side_effect=OSError('injected flush failure')):
    runpy.run_path('bin/group', run_name='__main__')
""", ok=False)
        self.assertEqual(json.loads(path.read_text())["group"]["id"], self.group["id"])
        self.assertEqual(self.calls(), [])
