"""Pinned candidate discovery is local, bounded and never a duplicate verdict."""

import copy
import json
import subprocess
import sys


from support import CandidateFixture


class CandidateTests(CandidateFixture):
    def test_direct_sets_and_frozen_draft_provenance_do_not_change_decisions(self):
        snapshot = self.publish([dict(files=["src/shared.py"]), dict(files=["src/shared.py"])])
        before = {str(path): path.read_bytes() for path in (self.root / "data").rglob("*") if path.is_file()}
        report = self.report(snapshot)
        self.assertEqual(before, {str(path): path.read_bytes() for path in (self.root / "data").rglob("*") if path.is_file()})
        suggestion = report["results"][0]
        self.assertEqual(suggestion["members"], [1, 2])
        self.assertIsNone(suggestion["survivor"])
        self.assertEqual(suggestion["pairs"][0]["signals"][0]["signal"], "files")
        group = json.loads(self.save_candidate(report).stdout)
        self.assertEqual(group["status"], "draft")
        self.assertNotIn("comparison", group)
        self.assertNotIn("reviewed", group)
        self.assertEqual(group["candidate_origin"]["suggestion"], suggestion)
        self.assertIn("Pinned candidate origin", self.cli("group", "export", group["id"]).stdout)
        self.cli("group", "remove", group["id"], "--kind", "pr", "--number", "2", "--revision", "1", "--by", "reviewer")
        edited = json.loads(self.cli("group", "show", group["id"]).stdout)
        self.assertEqual(edited["candidate_origin"], group["candidate_origin"])
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())
        self.assertEqual(self.calls(), [])

    def test_nontransitive_chain_and_negative_edges_never_flatten_into_set(self):
        snapshot = self.publish([dict(files=["a"]), dict(files=["a", "b"]), dict(files=["b"])])
        report = self.report(snapshot)
        self.assertEqual([row["members"] for row in report["results"]], [[1, 2], [2, 3]])
        (self.root / "data/owner/repo/ledger.jsonl").write_text(' {"kind":"pr","number":1}\n{"kind":"pr","number":2}\n')
        self.cli("not-duplicate", "--key", "pr:1", "--key", "pr:2", "--by", "reviewer", "--note", "Different behavior")
        blocked = self.report(snapshot)
        self.assertEqual(blocked["candidate_sets"], 1)
        self.assertEqual(blocked["results"][0]["members"], [2, 3])
        self.assertEqual(blocked["results"][1]["negative_verdicts"][0]["note"], "Different behavior")
        self.save_candidate(report, ok=False)
        changed = self.publish([dict(files=["a"], base="c" * 40), dict(files=["a", "b"]), dict(files=["b"])])
        self.assertEqual(self.report(changed)["excluded_pairs"], 1, "unbound legacy verdict is never silently expired")
        self.assertEqual(self.calls(), [])

    def test_cross_branch_candidates_and_base_drift_stay_explicit(self):
        snapshot = self.publish([dict(files=["a"]), dict(files=["a"], base="c" * 40), dict(files=["a"], branch="other")])
        report = self.report(snapshot)
        self.assertEqual(report["candidate_sets"], 1)
        self.assertEqual(report["excluded_pairs"], 2)
        self.assertIn("base-revisions-require-reconciliation", report["results"][0]["holds"])
        self.assertTrue(all("different-base-branches" in row["reasons"] for row in report["results"][1:]))

    def test_partial_missing_closed_and_unbound_sources_are_not_negative_verdicts(self):
        snapshot = self.publish([dict(title="Exact same title", missing="files"), dict(title="Exact same title", partial="closing_issues"),
                                 dict(title="Exact same title", partial="summary"), dict(state="closed"),
                                 dict(summary=dict(id=999)), dict(kind="issue")])
        report = self.report(snapshot)
        self.assertEqual(report["results"][0]["members"], [1, 2])
        self.assertIn("discovery-evidence-gap:files", report["results"][0]["holds"])
        self.assertEqual(sum(bool(row["exclusions"]) for row in report["observations"]), 4)
        old = self.report(snapshot, "--as-of", "2099-01-01T00:00:00Z")
        self.assertEqual(old["candidate_sets"], 1)
        self.assertIn("discovery-evidence-stale:summary", old["results"][0]["holds"])
        self.assertEqual(self.calls(), [])

    def test_title_links_and_broad_signals_have_separate_reasons(self):
        snapshot = self.publish([dict(title="Same operative behavior", files=["package-lock.json", "generated/foo"], issues=[42]),
                                 dict(title="Same operative behavior", files=["package-lock.json", "generated/foo"], issues=[42]),
                                 dict(files=["package-lock.json"], issues=[42])])
        report = self.report(snapshot, "--max-frequency", "2")
        self.assertEqual(report["candidate_sets"], 1)
        self.assertEqual([s["signal"] for s in report["results"][0]["pairs"][0]["signals"]], ["title"])
        self.assertEqual([s["total"] for s in report["suppressed_signals"]], [2, 1])
        report = self.report(snapshot)
        self.assertEqual(report["results"][0]["members"], [1, 2, 3])
        self.assertTrue(any(s["signal"] == "closing_issues" for s in report["results"][0]["pairs"][0]["signals"]))

    def test_pagination_binds_options_time_payloads_and_entire_scope(self):
        snapshot = self.publish([dict(files=["a"]), dict(files=["a", "b"]), dict(files=["b"])])
        first = self.report(snapshot, "--limit", "1")
        args = [part for key, value in first["continuation"].items() if key not in ("snapshot", "corpus") and value is not None
                for part in ("--" + key.replace("_", "-"), str(value))]
        self.assertEqual(self.report(snapshot, *args)["results"][0]["members"], [2, 3])
        self.cli("cache", "candidates", "--snapshot", snapshot, *args, "--max-frequency", "2", ok=False)
        ref = first["observations"][2]["components"]["summary"]["descriptor"]["object"]
        path = next((self.root / "data/owner/repo/cache/objects").glob(ref["sha256"] + "*"))
        path.unlink()
        self.cli("cache", "candidates", "--snapshot", snapshot, *args, ok=False)
        self.save_candidate(first, ok=False)
        changed = self.report(snapshot)
        self.assertTrue(changed["observations"][2]["exclusions"])
        self.assertEqual(self.calls(), [])

    def test_invalid_corrupt_and_future_packets_fail_without_publication(self):
        snapshot = self.publish([dict(files=["a"]), dict(files=["a"])])
        report = self.report(snapshot)
        bad = copy.deepcopy(report)
        bad["schema_version"] = 2
        self.save_candidate(bad, ok=False)
        bad = copy.deepcopy(report)
        bad["results"][0]["survivor"] = 1
        group = json.loads(self.save_candidate(bad).stdout)
        self.assertIsNone(group["candidate_origin"]["suggestion"]["survivor"], "saving reconstructs rather than trusts supplied results")
        path = self.root / "data/owner/repo/groups" / (group["id"] + ".json")
        group["candidate_origin"]["schema_version"] = 2
        path.write_text(json.dumps(group))
        self.cli("group", "show", group["id"], ok=False)
        for args in (("--limit", "101"), ("--offset", "1"), ("--title-threshold", "nan"), ("--max-frequency", "1"), ("--max-age", "-1")):
            self.cli("cache", "candidates", "--snapshot", snapshot, *args, ok=False)
        ref = report["observations"][0]["components"]["files"]["descriptor"]["object"]
        next((self.root / "data/owner/repo/cache/objects").glob(ref["sha256"] + "*")).write_text("corrupt")
        result = self.cli("cache", "candidates", "--snapshot", snapshot, ok=False)
        self.assertEqual(result.stdout, "")
        self.assertEqual(self.calls(), [])

    def test_28_members_keep_all_direct_pairs_without_top_ten_truncation(self):
        snapshot = self.publish([dict(title="Same shared behavior") for _ in range(28)])
        report = self.report(snapshot, "--limit", "1")
        suggestion = report["results"][0]
        self.assertEqual(len(suggestion["members"]), 28)
        self.assertEqual(len(suggestion["pairs"]), 378)
        self.assertEqual(report["candidate_sets"], 1)
        self.assertIsNone(report["continuation"])
        compact = self.report(snapshot, "--limit", "1", "--compact")
        self.assertEqual(compact["results"][0]["id"], suggestion["id"])
        self.assertEqual(len(compact["results"][0]["pairs"]), 20)
        self.assertEqual(compact["results"][0]["pair_count"], 378)
        group = json.loads(self.save_candidate(compact).stdout)
        self.assertEqual(len(group["candidate_origin"]["suggestion"]["pairs"]), 378)

    def test_corpus_progress_is_pinned_and_pending_never_falls_back(self):
        identifier, _ = self.create()
        before = len(self.calls())
        pending = json.loads(self.cli("cache", "candidates", "--corpus", identifier).stdout)
        self.assertEqual(pending["candidate_sets"], 0)
        self.assertTrue(all(row["exclusions"] for row in pending["observations"]))
        self.assertEqual(len(self.calls()), before)
        self.run_corpus(identifier)
        before = len(self.calls())
        self.cli("cache", "candidates", "--corpus", identifier, "--checkpoint", pending["checkpoint"], "--as-of", pending["options"]["as_of"], ok=False)
        report = json.loads(self.cli("cache", "candidates", "--corpus", identifier).stdout)
        self.assertEqual(report["candidate_sets"], 1)
        self.assertEqual(len(self.calls()), before)

    def test_scope_and_feature_limits_are_explicit(self):
        snapshot = self.publish([dict(files=[f"path{n}" for n in range(1001)]), dict(files=["path0"])])
        report = self.report(snapshot)
        self.assertEqual(report["candidate_sets"], 0)
        self.assertIn("discovery-feature-limit:files", report["observations"][0]["holds"])
        self.assertEqual(self.calls(), [])

    def test_failed_draft_publication_and_byte_budget_never_publish_partial_groups(self):
        snapshot = self.publish([dict(files=["a"]), dict(files=["a"])])
        packet = self.report(snapshot)
        (self.root / "candidate-packet.json").write_text(json.dumps(packet))
        script = """
import json, sys
sys.path.insert(0, 'bin')
from unittest.mock import patch
from _candidates import create_from_packet
from _groups import locked, save_group
packet = json.load(open('candidate-packet.json'))
with locked():
    group = create_from_packet(packet, packet['results'][0]['id'], 'reviewer')
    with patch('_storage.os.replace', side_effect=OSError('injected candidate publication failure')):
        save_group(group)
"""
        result = subprocess.run([sys.executable, "-c", script], cwd=self.root, env=self.env, text=True, capture_output=True)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("injected candidate publication failure", result.stderr)
        self.assertEqual(list((self.root / "data/owner/repo/groups").glob("*.json")), [])
        script = """
import json, sys
sys.path.insert(0, 'bin')
from unittest.mock import patch
from _cache import EvidenceCache
from _candidates import discover
packet = json.load(open('candidate-packet.json'))
cache = EvidenceCache(packet['source']['repository'])
with patch('_candidates.MAX_BYTES', 1), patch.object(cache, 'read_object', side_effect=AssertionError('must check budget before reading')):
    try:
        discover(cache, **packet['options'])
    except ValueError as error:
        assert 'payload budget' in str(error)
    else:
        raise AssertionError('budget was not enforced')
"""
        result = subprocess.run([sys.executable, "-c", script], cwd=self.root, env=self.env, text=True, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.calls(), [])
