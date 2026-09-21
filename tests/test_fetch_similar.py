"""Incremental fetch/sync, batch IDs, duplicate candidates, and per-repo data folders, in isolated checkouts with a fake gh; never touches real data or GitHub."""

import json
import os
import shutil
import subprocess
import tempfile
import unittest
from datetime import datetime, timedelta, timezone
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

# The fake gh logs every call and answers `gh api` with whatever JSON lines the test put in gh_response.jsonl; `gh issue/pr view` (enrichment) gets a fixed body.
FAKE_GH = """#!/usr/bin/env python3
import json, os, sys
root = os.environ["FAKE_GH_DIR"]
with open(os.path.join(root, "gh_calls.log"), "a") as f:
    f.write(json.dumps(sys.argv[1:]) + "\\n")
if sys.argv[1] == "api":
    print(open(os.path.join(root, "gh_response.jsonl")).read(), end="")
else:
    print(json.dumps({"body": "Body", "comments": [{"body": "A comment", "author": {"login": "commenter"}}]}))
"""


def item(number, title, state="open", kind="issue"):
    return {"number": number, "kind": kind, "title": title, "url": "", "author": "someone", "created_at": f"2025-01-{number:02d}T00:00:00Z", "updated_at": "", "state": state, "state_reason": None, "labels": [], "comments_count": 0}


class CheckoutTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        shutil.copytree(ROOT / "bin", self.root / "bin", ignore=shutil.ignore_patterns("triage-o-mator", "__pycache__"))
        shutil.copytree(ROOT / "config", self.root / "config")
        # What bin/install-to writes to mark an install; the scripts refuse to run anywhere else.
        (self.root / ".triage-install.json").write_text('{"tool": "%s"}\n' % ROOT)
        (self.root / "config/repo").write_text("owner/repo\n")
        (self.root / "data/owner/repo").mkdir(parents=True)
        mock = self.root / "mock"
        mock.mkdir()
        (mock / "gh").write_text(FAKE_GH)
        (mock / "gh").chmod(0o755)
        self.env = dict(os.environ, PATH=str(mock) + os.pathsep + os.environ["PATH"], FAKE_GH_DIR=str(mock))
        self.mock = mock

    def respond(self, items):
        (self.mock / "gh_response.jsonl").write_text("".join(json.dumps(i) + "\n" for i in items))

    def run_cli(self, command, *args):
        result = subprocess.run([str(self.root / "bin" / command), *args], cwd=self.root, env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)

        return result.stdout + result.stderr

    def last_endpoint(self):
        calls = [json.loads(l) for l in (self.mock / "gh_calls.log").read_text().splitlines()]

        return [c for c in calls if c[0] == "api"][-1][2]

    def meta(self):
        return json.loads((self.root / "data/owner/repo/raw/fetch_meta.json").read_text())

    def set_meta(self, **changes):
        meta = self.meta()
        meta.update(changes)
        (self.root / "data/owner/repo/raw/fetch_meta.json").write_text(json.dumps(meta))

    def ledger(self):
        path = self.root / "data/owner/repo/ledger.jsonl"

        return {(r["kind"], r["number"]): r for r in map(json.loads, path.read_text().splitlines())}


class IncrementalFetchTests(CheckoutTest):
    def full_sync(self, items):
        self.respond(items)
        self.run_cli("fetch")
        self.assertIn("state=open", self.last_endpoint())
        self.run_cli("sync")

    def test_first_fetch_is_full_then_incremental_since_last_sync(self):
        self.full_sync([item(1, "one"), item(2, "two"), item(3, "three")])
        meta = self.meta()
        self.assertEqual(meta["synced_through"], meta["fetched_at"])
        self.assertEqual(meta["last_full_at"], meta["fetched_at"])

        self.respond([item(2, "two renamed", state="closed"), item(4, "four"), item(9, "closed before we ever saw it", state="closed")])
        out = self.run_cli("fetch")
        endpoint = self.last_endpoint()
        self.assertIn("state=all", endpoint)
        since = datetime.strptime(meta["synced_through"], "%Y-%m-%dT%H:%M:%SZ") - timedelta(minutes=5)
        self.assertIn("since=" + since.strftime("%Y-%m-%dT%H:%M:%SZ"), endpoint)
        self.assertIn("incremental", out)
        self.run_cli("sync")

        ledger = self.ledger()
        self.assertEqual(ledger[("issue", 1)]["state"], "open", "incremental sync must not close items it simply didn't hear about")
        self.assertEqual((ledger[("issue", 2)]["state"], ledger[("issue", 2)]["title"]), ("closed", "two renamed"))
        self.assertEqual(ledger[("issue", 4)]["state"], "open")
        self.assertNotIn(("issue", 9), ledger)
        self.assertEqual(self.meta()["mode"], "incremental")

    def test_full_fetch_closes_vanished_items(self):
        self.full_sync([item(1, "one"), item(2, "two")])
        self.respond([item(1, "one")])
        self.run_cli("fetch", "--full")
        self.assertIn("state=open", self.last_endpoint())
        self.run_cli("sync")
        self.assertEqual(self.ledger()[("issue", 2)]["state"], "closed")

    def test_falls_back_to_full(self):
        self.full_sync([item(1, "one")])
        stale = (datetime.now(timezone.utc) - timedelta(days=2)).strftime("%Y-%m-%dT%H:%M:%SZ")
        self.set_meta(last_full_at=stale)
        self.assertIn("over a day old", self.run_cli("fetch"))
        self.assertIn("state=open", self.last_endpoint())

        self.assertIn("never synced", self.run_cli("fetch"), "an unsynced full fetch must not be followed by an incremental one")

        self.run_cli("sync")
        self.set_meta(repo="someone/else")
        self.assertIn("no previous fetch for this repo", self.run_cli("fetch"))


class BatchAndSimilarTests(CheckoutTest):
    def setUp(self):
        super().setUp()
        rows = [
            item(1, "Waybar crashes on suspend with NVIDIA driver"),
            item(2, "Waybar crash on suspend with the NVIDIA driver"),
            item(3, "Font picker excludes monospace fonts"),
            item(4, "Waybar crashes on suspend with NVIDIA driver", kind="pr"),
        ]
        for r in rows:
            r.update(category="", action="", confidence="", reason="", reviewed=False)

        (self.root / "data/owner/repo/ledger.jsonl").write_text("".join(json.dumps(r) + "\n" for r in rows))

    def test_similar_ranks_same_kind_and_skips_self(self):
        out = json.loads(self.run_cli("similar", "--kind", "issue", "--number", "1"))
        self.assertEqual([c["number"] for c in out["candidates"]], [2])
        self.assertGreater(out["candidates"][0]["score"], 0.6)
        anykind = json.loads(self.run_cli("similar", "--kind", "issue", "--number", "1", "--any-kind"))
        self.assertEqual({c["number"] for c in anykind["candidates"]}, {2, 4})
        self.assertFalse((self.mock / "gh_calls.log").exists(), "similar without --enrich must stay offline")

    def test_pairs_lists_each_open_same_kind_pair_once_newer_first(self):
        pairs = json.loads(self.run_cli("similar", "--pairs"))["pairs"]
        self.assertEqual([(p["item"]["number"], p["original"]["number"]) for p in pairs], [(2, 1)])
        self.assertGreater(pairs[0]["score"], 0.6)
        rows = [json.loads(l) for l in (self.root / "data/owner/repo/ledger.jsonl").read_text().splitlines()]
        rows[0]["state"] = "closed"
        (self.root / "data/owner/repo/ledger.jsonl").write_text("".join(json.dumps(r) + "\n" for r in rows))
        self.assertEqual(json.loads(self.run_cli("similar", "--pairs"))["pairs"], [], "closed items should not form pairs")

    def test_batch_ids_are_unique_and_items_carry_candidates(self):
        first = self.run_cli("batch", "2", "--kind", "issue")
        second = self.run_cli("batch", "2", "--kind", "issue")
        ids = [line.split()[1].rstrip(":") for line in (first + second).splitlines() if line.startswith("Batch ")]
        self.assertEqual(len(set(ids)), 2, ids)
        items = [json.loads(l) for l in (self.root / "data/owner/repo/batches" / f"{ids[0]}.items.jsonl").read_text().splitlines()]
        by_number = {i["number"]: i for i in items}
        self.assertEqual([c["number"] for c in by_number[1]["duplicate_candidates"]], [2])

    def test_delete_batch_removes_only_that_batch(self):
        out = self.run_cli("batch", "2", "--kind", "issue") + self.run_cli("batch", "1", "--kind", "issue")
        ids = [line.split()[1].rstrip(":") for line in out.splitlines() if line.startswith("Batch ")]
        self.run_cli("batch", "--delete", ids[0])
        remaining = sorted(p.name for p in (self.root / "data/owner/repo/batches").iterdir())
        self.assertEqual(remaining, [f"{ids[1]}.decisions.jsonl", f"{ids[1]}.items.jsonl"])
        for bad in ["../ledger", "b20260101-000000/../../x", ids[0]]:
            result = subprocess.run([str(self.root / "bin/batch"), "--delete", bad], cwd=self.root, env=self.env, capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0, bad)

        self.assertTrue((self.root / "data/owner/repo/ledger.jsonl").exists())

    def test_pairs_ruled_out_stay_out_but_new_similar_items_still_pair(self):
        verdict = json.loads(self.run_cli("not-duplicate", "--key", "issue:2", "--key", "issue:1", "--by", "Ada", "--note", "different GPU"))
        # The pair is stored by its two numbers, smallest first, so which was compared against which doesn't matter.
        self.assertEqual((verdict["kind"], verdict["a"], verdict["b"], verdict["by"], verdict["note"]), ("issue", 1, 2, "Ada", "different GPU"))
        self.assertEqual(json.loads(self.run_cli("similar", "--pairs"))["pairs"], [])
        kept = json.loads(self.run_cli("similar", "--pairs", "--include-checked"))["pairs"]
        self.assertEqual([(p["item"]["number"], p["original"]["number"]) for p in kept], [(2, 1)])

        # A third report of the same thing pairs with each of them, and those pairs have not been ruled out.
        rows = (self.root / "data/owner/repo/ledger.jsonl").read_text()
        new = item(5, "Waybar crashes on suspend with the NVIDIA driver")
        new.update(category="", action="", confidence="", reason="", reviewed=False)
        (self.root / "data/owner/repo/ledger.jsonl").write_text(rows + json.dumps(new) + "\n")
        pairs = json.loads(self.run_cli("similar", "--pairs"))["pairs"]
        self.assertEqual({(p["item"]["number"], p["original"]["number"]) for p in pairs}, {(5, 1), (5, 2)})

        # Taking the verdict back offers the pair again; the ledger is never touched by any of this.
        before = (self.root / "data/owner/repo/ledger.jsonl").read_text()
        self.run_cli("not-duplicate", "--remove", "--key", "issue:1", "--key", "issue:2")
        pairs = json.loads(self.run_cli("similar", "--pairs"))["pairs"]
        self.assertIn((2, 1), {(p["item"]["number"], p["original"]["number"]) for p in pairs})
        self.assertEqual(json.loads(self.run_cli("not-duplicate", "--list")), [])
        self.assertEqual((self.root / "data/owner/repo/ledger.jsonl").read_text(), before)

    def test_not_duplicate_refuses_nonsense_pairs(self):
        for args in (
            ["--key", "issue:1", "--by", "Ada"],
            ["--key", "issue:1", "--key", "pr:4", "--by", "Ada"],
            ["--key", "issue:1", "--key", "issue:1", "--by", "Ada"],
            ["--key", "issue:1", "--key", "issue:999", "--by", "Ada"],
            ["--key", "issue:1", "--key", "issue:2"],
            ["--remove", "--key", "issue:1", "--key", "issue:2"],
        ):
            result = subprocess.run([str(self.root / "bin/not-duplicate"), *args], cwd=self.root, env=self.env, capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0, args)

        self.assertFalse((self.root / "data/owner/repo/not-duplicates.jsonl").exists())


class RepoDataTests(CheckoutTest):
    """Each repo's data lives in its own data/<owner>/<repo>/ folder, and switching repos never mixes two of them."""

    def test_switching_repos_keeps_each_ledger_separate(self):
        self.respond([item(1, "first repo's issue")])
        self.run_cli("fetch")
        self.run_cli("sync")
        first = (self.root / "data/owner/repo/ledger.jsonl").read_bytes()

        (self.root / "config/repo").write_text("other/repo\n")
        self.respond([item(1, "second repo's issue #1"), item(2, "second repo's issue #2")])
        self.assertIn("no previous fetch for this repo", self.run_cli("fetch"))
        self.run_cli("sync")

        self.assertEqual(first, (self.root / "data/owner/repo/ledger.jsonl").read_bytes(), "the first repo's ledger must be untouched")
        second = [json.loads(l) for l in (self.root / "data/other/repo/ledger.jsonl").read_text().splitlines()]
        self.assertEqual([r["title"] for r in second], ["second repo's issue #1", "second repo's issue #2"])

    def test_repo_names_cannot_escape_data(self):
        for bad in ["../evil", "owner/..", "owner", "a/b/c"]:
            (self.root / "config/repo").write_text(bad + "\n")
            result = subprocess.run([str(self.root / "bin/stats")], cwd=self.root, env=self.env, capture_output=True, text=True)
            self.assertNotEqual(result.returncode, 0, bad)
            self.assertIn("does not look like 'owner/repo'", result.stderr)


if __name__ == "__main__":
    unittest.main()
