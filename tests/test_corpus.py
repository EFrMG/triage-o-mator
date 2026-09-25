"""Frozen selections, shared budgets, interrupted runners and offline progress in fake installs."""

import json
import subprocess
import sys
import copy
import fcntl
from hashlib import sha256


from support import CorpusFixture


class CorpusTests(CorpusFixture):
    def test_dataset_update_reuses_existing_components_and_fetches_new_items(self):
        prior, _ = self.create([self.row("pr", 1)])
        first = self.run_corpus(prior)
        old_snapshot = first["progress"]["items"]["pr:1"]["snapshot_id"]
        self.fetch_inventory([self.row("pr", 1), self.row("pr", 2)])
        inventory = json.loads(self.cli("cache", "import-inventory").stdout)["snapshot_id"]
        created = json.loads(self.cli("cache", "corpus-create", "--snapshot", inventory, "--profile", "discussion", "--reuse-corpus", prior).stdout)
        identifier = created["corpus_id"]
        before = len(self.calls())
        result = self.run_corpus(identifier)
        endpoints = [call[-1] for call in self.calls()[before:]]
        self.assertEqual(result["counts"]["complete"], 2)
        self.assertEqual(result["plan"]["reuse_snapshots"], {"pr:1": old_snapshot})
        self.assertNotIn("repos/owner/repo/issues/1/comments?per_page=100&page=1", endpoints)
        self.assertIn("repos/owner/repo/issues/2/comments?per_page=100&page=1", endpoints)
        self.assertIn("repos/owner/repo/pulls/1", endpoints, "reuse must recheck current PR metadata")
        self.assertNotEqual(result["progress"]["items"]["pr:1"]["snapshot_id"], old_snapshot)
        self.assertEqual(self.status(prior)["progress"], first["progress"])

    def test_bulk_backlog_publishes_graphql_comments_and_local_diff(self):
        identifier, _ = self.create([self.row("pr", 1)], profile="backlog")
        (self.mock / "gh").write_text('''#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
root = Path(os.environ["FAKE_GH_DIR"])
payload = json.load(sys.stdin)
from pathlib import Path
sys.path.insert(0, str(root.parent / "bin"))
from _bulk import BATCH_SIZE, QUERIES
assert payload["query"] in QUERIES.values() and "mutation" not in payload["query"]
with (root / "bulk_calls.log").open("a") as out:
    out.write(json.dumps(payload) + "\\n")
initial = "files(first: 100)" in payload["query"]
node = dict(id="PR_1", databaseId=101, number=1, url="https://github.com/owner/repo/pull/1", state="OPEN", updatedAt="2026-09-22T01:00:00Z",
            baseRefOid="a" * 40, headRefOid="b" * 40, baseRefName="main", headRefName="fix",
            baseRepository=dict(id="R_repo", databaseId=42, nameWithOwner="owner/repo"), headRepository=dict(id="R_repo", databaseId=42, nameWithOwner="owner/repo"),
            changedFiles=1, additions=1, deletions=0, isDraft=False, mergeable="MERGEABLE")
if initial:
    node.update(title="A contribution", body="Body with evidence", comments=dict(totalCount=0, pageInfo=dict(hasNextPage=False, endCursor=None), nodes=[]),
                files=dict(totalCount=1, pageInfo=dict(hasNextPage=False, endCursor=None), nodes=[dict(path="test.txt", additions=1, deletions=0, changeType="MODIFIED")]),
                closingIssuesReferences=dict(totalCount=1, pageInfo=dict(hasNextPage=False, endCursor=None), nodes=[dict(id="I_9", number=9, url="https://github.com/owner/repo/issues/9", state="CLOSED", updatedAt="2026-09-22T00:00:00Z", repository=dict(id="R_repo", nameWithOwner="owner/repo"))]))
else:
    node.update(comments=dict(totalCount=0), closingIssuesReferences=dict(totalCount=1))
repo = dict(id="R_repo", databaseId=42, nameWithOwner="owner/repo", **{f"p{i}": node for i in range(BATCH_SIZE)})
print("HTTP/2.0 200\\n")
print(json.dumps(dict(data=dict(repository=repo, rateLimit=dict(remaining=100, resetAt="2026-09-24T23:00:00Z")))))
''')
        (self.mock / "git").write_text('''#!/usr/bin/env python3
import os, sys
from pathlib import Path
args = sys.argv[1:]
root = Path(os.environ["FAKE_GH_DIR"])
with (root / "git_calls.log").open("a") as out:
    out.write(" ".join(args) + "\\n")
if "rev-parse" in args:
    print(root.parent.parent)
elif "diff" in args:
    print("diff --git a/test.txt b/test.txt\\nindex aaaaaaa..bbbbbbb 100644\\n--- a/test.txt\\n+++ b/test.txt\\n@@ -1 +1,2 @@\\n base\\n+added")
''')
        (self.mock / "gh").chmod(0o755)
        (self.mock / "git").chmod(0o755)
        result = json.loads(self.cli("cache", "corpus-run", identifier, "--bulk", "--request-budget", "3", "--compact").stdout)
        self.assertEqual(result["declared_counts"]["complete"], 1)
        self.assertEqual(result["last_run"]["requests"], 3)
        read = json.loads(self.cli("cache", "read", "--kind", "pr", "--number", "1", "--profile", "backlog").stdout)
        self.assertEqual(read["data"]["files"][0]["patch"], "@@ -1 +1,2 @@\n base\n+added")
        self.assertEqual(read["components"]["diff"]["source"]["transport"], "git")
        self.assertEqual(read["data"]["closing_issues"][0]["identity"]["number"], 9)
        self.assertEqual(len((self.mock / "bulk_calls.log").read_text().splitlines()), 2)
        self.assertEqual(len([line for line in (self.mock / "git_calls.log").read_text().splitlines() if " fetch " in line]), 1)

        # A path the text-diff verifier cannot parse still has a complete GraphQL file list for duplicate discovery.
        (self.mock / "gh").write_text((self.mock / "gh").read_text().replace("test.txt", "guide notes.txt"))
        (self.mock / "git").write_text((self.mock / "git").read_text().replace("test.txt", '"guide notes.txt"'))
        inventory = self.status(identifier)["plan"]["inventory_snapshot"]
        another = json.loads(self.cli("cache", "corpus-create", "--snapshot", inventory, "--scope", "open-prs", "--profile", "backlog", "--max-age", "0").stdout)["corpus_id"]
        result = json.loads(self.cli("cache", "corpus-run", another, "--bulk", "--request-budget", "3", "--compact").stdout)
        self.assertEqual(result["declared_counts"]["gaps"], 1)
        read = json.loads(self.cli("cache", "read", "--kind", "pr", "--number", "1", "--profile", "backlog").stdout)
        self.assertEqual(read["components"]["files"]["status"], "complete")
        self.assertEqual(read["data"]["files"][0]["filename"], "guide notes.txt")
        self.assertEqual(read["components"]["diff"]["status"], "partial")

    def test_bulk_backlog_issues_need_no_git_fetch(self):
        identifier, _ = self.create([self.row("issue", 1)], scope="open-issues", profile="backlog")
        (self.mock / "gh").write_text('''#!/usr/bin/env python3
import json, sys
payload = json.load(sys.stdin)
assert "fragment Detail on Issue" in payload["query"] and "mutation" not in payload["query"]
from pathlib import Path
import os
sys.path.insert(0, str(Path(os.environ["FAKE_GH_DIR"]).parent / "bin"))
from _bulk import BATCH_SIZE
initial = "comments(first: 100)" in payload["query"]
node = dict(id="LIST_1", databaseId=1001, number=1, url="https://github.com/owner/repo/issues/1", state="OPEN", updatedAt="2026-09-22T01:00:00Z")
if initial:
    node.update(title="A contribution", body="Body with evidence", comments=dict(totalCount=1, pageInfo=dict(hasNextPage=False, endCursor=None),
                nodes=[dict(id="C_1", databaseId=1, url="https://github.com/owner/repo/issues/1#issuecomment-1", body="Useful context",
                            createdAt="2026-09-22T00:00:00Z", updatedAt="2026-09-22T00:00:00Z", author=dict(login="reviewer"))]))
else:
    node["comments"] = dict(totalCount=1)
repo = dict(id="R_repo", databaseId=42, nameWithOwner="owner/repo", **{f"p{i}": node for i in range(BATCH_SIZE)})
print("HTTP/2.0 200\\n")
print(json.dumps(dict(data=dict(repository=repo, rateLimit=dict(remaining=100, resetAt="2026-09-24T23:00:00Z")))))
''')
        (self.mock / "gh").chmod(0o755)
        result = json.loads(self.cli("cache", "corpus-run", identifier, "--bulk", "--request-budget", "2", "--compact").stdout)
        self.assertEqual(result["declared_counts"]["complete"], 1)
        self.assertEqual(result["last_run"]["requests"], 2)
        self.assertFalse((self.mock / "git_calls.log").exists())
        read = json.loads(self.cli("cache", "read", "--kind", "issue", "--number", "1", "--profile", "backlog").stdout)
        self.assertEqual(read["data"]["comments"][0]["body"], "Useful context")
        self.assertEqual(read["components"]["diff"]["status"], "not_applicable")

    def test_bulk_502_splits_only_failed_batch_and_rechecks_smaller_queries(self):
        identifier, _ = self.create([self.row("issue", 1), self.row("issue", 2)], scope="open-issues", profile="backlog")
        (self.mock / "gh").write_text('''#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
root = Path(os.environ["FAKE_GH_DIR"])
payload = json.load(sys.stdin)
sys.path.insert(0, str(root.parent / "bin"))
from _bulk import QUERIES
assert payload["query"] in QUERIES.values() and "mutation" not in payload["query"]
numbers = [value for key, value in payload["variables"].items() if key.startswith("n")]
initial = "comments(first: 100)" in payload["query"]
with (root / "bulk_calls.log").open("a") as out:
    out.write(json.dumps(dict(width=len(numbers), initial=initial)) + "\\n")
if len(numbers) == 2 and not initial:
    print("HTTP/2.0 502\\n")
    sys.exit(1)
repo = dict(id="R_repo", databaseId=42, nameWithOwner="owner/repo")
for index, number in enumerate(numbers):
    node = dict(id=f"LIST_{number}", databaseId=1000 + number, number=number, url=f"https://github.com/owner/repo/issues/{number}",
                state="OPEN", updatedAt="2026-09-22T01:00:00Z")
    if initial:
        node.update(title=f"Issue {number}", body="Body with evidence", comments=dict(totalCount=0, pageInfo=dict(hasNextPage=False, endCursor=None), nodes=[]))
    else:
        node["comments"] = dict(totalCount=0)
    repo[f"p{index}"] = node
print("HTTP/2.0 200\\n")
print(json.dumps(dict(data=dict(repository=repo, rateLimit=dict(remaining=100, resetAt="2026-09-24T23:00:00Z")))))
''')
        (self.mock / "gh").chmod(0o755)
        result = json.loads(self.cli("cache", "corpus-run", identifier, "--bulk", "--request-budget", "6", "--compact").stdout)
        self.assertEqual(result["status"], "finished", result)
        self.assertEqual(result["declared_counts"]["complete"], 2)
        self.assertEqual(result["declared_counts"]["error"], 0)
        self.assertEqual(result["last_run"]["requests"], 6)
        calls = [json.loads(line) for line in (self.mock / "bulk_calls.log").read_text().splitlines()]
        self.assertEqual([(call["width"], call["initial"]) for call in calls], [(2, True), (2, False), (1, True), (1, False), (1, True), (1, False)])
        for number in (1, 2):
            read = json.loads(self.cli("cache", "read", "--kind", "issue", "--number", str(number), "--profile", "backlog").stdout)
            self.assertEqual(read["data"]["summary"]["number"], number)

    def test_bulk_502_next_batch_returns_to_80(self):
        self.responses["repos/owner/repo/issues?state=open&per_page=100&page=1"] = dict(data=[self.row("issue", number) for number in range(1, 101)], headers={"Link": '<ignored>; rel="next"'})
        self.responses["repos/owner/repo/issues?state=open&per_page=100&page=2"] = dict(data=[self.row("issue", number) for number in range(101, 161)])
        (self.mock / "responses.json").write_text(json.dumps(self.responses))
        self.cli("fetch", "--cache-inventory")
        snapshot = json.loads(self.cli("cache", "import-inventory").stdout)["snapshot_id"]
        identifier = json.loads(self.cli("cache", "corpus-create", "--snapshot", snapshot, "--scope", "open-issues", "--profile", "backlog").stdout)["corpus_id"]
        (self.mock / "gh").write_text('''#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
root = Path(os.environ["FAKE_GH_DIR"])
payload = json.load(sys.stdin)
numbers = [payload["variables"][f"n{index}"] for index in range(len(payload["variables"]) - 2)]
initial = "comments(first: 100)" in payload["query"]
with (root / "bulk_calls.log").open("a") as out:
    out.write(json.dumps(dict(width=len(numbers), initial=initial, first=numbers[0])) + "\\n")
if len(numbers) == 80 and numbers[0] == 1 and initial:
    print("HTTP/2.0 502\\n")
    sys.exit(1)
repo = dict(id="R_repo", databaseId=42, nameWithOwner="owner/repo")
for index, number in enumerate(numbers):
    node = dict(id=f"LIST_{number}", databaseId=1000 + number, number=number, url=f"https://github.com/owner/repo/issues/{number}",
                state="OPEN", updatedAt="2026-09-22T01:00:00Z")
    if initial:
        node.update(title=f"Issue {number}", body="Body with evidence", comments=dict(totalCount=0, pageInfo=dict(hasNextPage=False, endCursor=None), nodes=[]))
    else:
        node["comments"] = dict(totalCount=0)
    repo[f"p{index}"] = node
print("HTTP/2.0 200\\n")
print(json.dumps(dict(data=dict(repository=repo, rateLimit=dict(remaining=100, resetAt="2026-09-24T23:00:00Z")))))
''')
        (self.mock / "gh").chmod(0o755)
        result = json.loads(self.cli("cache", "corpus-run", identifier, "--bulk", "--request-budget", "7", "--compact").stdout)
        self.assertEqual(result["status"], "finished", result)
        self.assertEqual(result["declared_counts"]["complete"], 160)
        self.assertEqual(result["last_run"]["requests"], 7)
        calls = [json.loads(line) for line in (self.mock / "bulk_calls.log").read_text().splitlines()]
        self.assertEqual([(call["width"], call["first"]) for call in calls], [(80, 1), (40, 1), (40, 1), (40, 41), (40, 41), (80, 81), (80, 81)])

    def test_dataset_update_does_not_replace_newer_partial_detail_with_donor(self):
        prior, _ = self.create([self.row("pr", 1)])
        self.run_corpus(prior)
        self.fetch_inventory([self.row("pr", 1)])
        inventory = json.loads(self.cli("cache", "import-inventory").stdout)["snapshot_id"]
        identifier = json.loads(self.cli("cache", "corpus-create", "--snapshot", inventory, "--profile", "discussion", "--reuse-corpus", prior).stdout)["corpus_id"]
        endpoint = "repos/owner/repo/issues/1/comments?per_page=100&page=1"
        self.responses[endpoint] = dict(status=503)
        self.run_cache("fetch", "pr", "discussion", "--mode", "refresh")
        before = len(self.calls())
        result = json.loads(self.cli("cache", "corpus-run", identifier).stdout)
        self.assertEqual(result["counts"]["gaps"], 1)
        self.assertIn(endpoint, [call[-1] for call in self.calls()[before:]])

    def test_dataset_reuse_rechecks_changes_and_preserves_budget_resume(self):
        prior, _ = self.create([self.row("pr", 1)])
        self.run_corpus(prior)
        self.fetch_inventory([self.row("pr", 1)])
        inventory = json.loads(self.cli("cache", "import-inventory").stdout)["snapshot_id"]
        created = json.loads(self.cli("cache", "corpus-create", "--snapshot", inventory, "--profile", "discussion", "--reuse-corpus", prior).stdout)
        identifier = created["corpus_id"]
        self.responses["repos/owner/repo/pulls/1"]["data"]["updated_at"] = "2026-09-24T00:00:00Z"
        (self.mock / "responses.json").write_text(json.dumps(self.responses))
        stopped = json.loads(self.cli("cache", "corpus-run", identifier, "--request-budget", "1").stdout)
        self.assertEqual(stopped["progress"]["status"], "stopped")
        self.assertEqual(stopped["progress"]["last_run"]["requests"], 1)
        before = len(self.calls())
        result = json.loads(self.cli("cache", "corpus-run", identifier, "--request-budget", "100").stdout)
        self.assertEqual(result["counts"]["complete"], 1)
        self.assertIn("repos/owner/repo/issues/1/comments?per_page=100&page=1", [call[-1] for call in self.calls()[before:]])
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())

    def test_bounded_progress_is_offline_and_does_not_audit_payloads(self):
        identifier, inventory = self.create()
        before = len(self.calls())
        pending = json.loads(self.cli("cache", "corpus-progress", identifier).stdout)
        self.assertEqual(pending["declared_counts"]["pending"], 2)
        self.assertEqual(pending["inventory_snapshot"], inventory)
        self.assertIsNone(pending["updated_at"])
        self.assertEqual(pending["inspection_requests"], 0)
        self.assertNotIn("items", pending)
        self.assertEqual(len(self.calls()), before)

        self.run_corpus(identifier)
        ref = self.listing(identifier)["items"][0]["components"]["summary"]["object"]
        target = next((self.root / "data/owner/repo/cache/objects").glob(ref["sha256"] + "*"))
        target.write_text("corrupt")
        before = len(self.calls())
        declared = json.loads(self.cli("cache", "corpus-progress", identifier).stdout)
        self.assertEqual(declared["status"], "finished")
        self.assertIn("no payload audit", declared["verification"])
        self.cli("cache", "corpus-status", identifier, ok=False)
        self.assertEqual(len(self.calls()), before)

    def test_abandoned_running_checkpoint_is_reported_as_interrupted(self):
        identifier, _ = self.create()
        self.run_corpus(identifier)
        target = self.root / "data/owner/repo/cache/corpora" / (identifier + ".state.json")
        state = json.loads(target.read_text())
        state["status"] = "running"
        state["items"]["pr:1"]["outcome"] = "running"
        state.pop("checksum")
        state["checksum"] = sha256(json.dumps(state, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
        target.write_text(json.dumps(state))

        before = len(self.calls())
        progress = json.loads(self.cli("cache", "corpus-progress", identifier).stdout)
        self.assertEqual(progress["status"], "interrupted")
        self.assertIn("runner exited", progress["last_run"]["reason"])
        self.assertEqual(self.listing(identifier)["status"], "interrupted")
        self.assertEqual(len(self.calls()), before)
        self.assertEqual(json.loads(target.read_text())["status"], "running", "status reads must not rewrite the checkpoint")

        lock_path = target.parent / "local" / (identifier + ".runner.lock")
        with lock_path.open("rb") as lock:
            fcntl.flock(lock, fcntl.LOCK_EX)
            try:
                active = json.loads(self.cli("cache", "corpus-progress", identifier).stdout)
                self.assertEqual(active["status"], "running")
            finally:
                fcntl.flock(lock, fcntl.LOCK_UN)

        candidates = json.loads(self.cli("cache", "candidates", "--corpus", identifier, "--compact").stdout)
        self.assertEqual(candidates["source"]["corpus"], identifier)
        self.assertEqual(len(self.calls()), before)

    def test_compact_run_reports_handled_stop_and_shared_budget(self):
        identifier, _ = self.create()
        self.seed_details()
        before = len(self.calls())
        result = json.loads(self.cli("cache", "corpus-run", identifier, "--request-budget", "1", "--compact").stdout)
        self.assertEqual(result["status"], "stopped")
        self.assertEqual(result["last_run"]["requests"], 1)
        self.assertEqual(result["last_run"]["request_budget"], 1)
        self.assertEqual(len(self.calls()) - before, 1)
        self.assertNotIn("progress", result)
        self.assertEqual(result["inspection_requests"], 0)

    def test_storage_limit_stops_with_partial_evidence_and_resumes(self):
        identifier, _ = self.create([self.row("pr", 1)])
        self.seed_details()
        saved_before = len(json.loads(self.cli("cache", "status").stdout)["snapshots"])
        script = """
import json, sys
sys.path.insert(0, 'bin')
import _cache, _corpus
from _evidence import repository
cache = _cache.EvidenceCache(repository('owner/repo'))
original = cache.publish
first = True
def publish(manifest, payloads):
    global first
    result = original(manifest, payloads)
    if first:
        first = False
        _cache.DATASET_LIMIT_BYTES = cache._budget_used() + _cache.DATASET_RESERVE_BYTES
    return result
cache.publish = publish
print(json.dumps(_corpus.run(cache, sys.argv[1], budget=100, compact=True)))
"""
        stopped = subprocess.run([sys.executable, "-c", script, identifier], cwd=self.root, env=self.env, capture_output=True, text=True, timeout=20)
        self.assertEqual(stopped.returncode, 0, stopped.stderr)
        progress = json.loads(stopped.stdout)
        self.assertEqual(progress["status"], "stopped")
        self.assertIn("storage limit", progress["last_run"]["reason"])
        first = self.status(identifier)
        self.assertEqual(first["progress"]["status"], "stopped")
        snapshots = json.loads(self.cli("cache", "status").stdout)["snapshots"]
        self.assertGreater(len(snapshots), saved_before)
        self.assertTrue(any(not snapshot["coverage"]["complete"] for snapshot in snapshots))
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())
        finished = self.run_corpus(identifier)
        self.assertEqual(finished["counts"]["complete"], 1)

    def test_corpus_target_guard_precedes_network_or_cache_mutation(self):
        before = len(self.calls())
        result = self.cli("cache", "--expected-repo", "foreign/repo", "corpus-run", "a" * 64, "--compact", ok=False)
        self.assertIn("repository changed", result.stderr)
        self.assertEqual(len(self.calls()), before)
        self.assertFalse((self.root / "data/owner/repo/cache").exists())

    def test_paginated_discovery_is_offline_and_pending_token_is_stable(self):
        identifier, inventory = self.create()
        before = len(self.calls())
        first = self.listing(identifier, "--limit", "1")
        self.assertEqual(first["declared_counts"]["pending"], 2)
        self.assertEqual(first["inventory_snapshot"], inventory)
        self.assertIsNone(first["updated_at"])
        self.assertIsNone(first["items"][0]["snapshot_id"])
        self.assertEqual(first["checkpoint"], self.listing(identifier)["checkpoint"])
        second = self.listing(identifier, "--offset", "1", "--limit", "1", "--checkpoint", first["checkpoint"])
        self.assertEqual(second["items"][0]["identity"]["number"], 2)
        self.assertIsNone(second["continuation"])
        self.assertEqual(second["pagination"]["omitted_before"], 1)
        self.cli("cache", "corpus-list", identifier, "--offset", "1", ok=False)
        for args in (("--limit", "101"), ("--offset", "-1"), ("--checkpoint", "bad"), ("--offset", "3", "--checkpoint", first["checkpoint"])):
            self.cli("cache", "corpus-list", identifier, *args, ok=False)

        self.assertEqual(len(self.calls()), before)

    def test_continuation_refuses_changed_progress_and_keeps_pinned_results(self):
        identifier, _ = self.create()
        pending = self.listing(identifier, "--limit", "1")
        result = self.run_corpus(identifier, 4)
        self.cli("cache", "corpus-list", identifier, "--offset", "1", "--checkpoint", pending["checkpoint"], ok=False)
        first = self.listing(identifier, "--limit", "1")
        self.assertEqual(first["items"][0]["snapshot_id"], result["progress"]["items"]["pr:1"]["snapshot_id"])
        self.assertEqual(first["items"][0]["outcome"], "complete")
        self.fetch_inventory([self.row("pr", 3)])
        self.assertEqual(self.listing(identifier, "--checkpoint", first["checkpoint"])["items"][0]["snapshot_id"], first["items"][0]["snapshot_id"])
        self.run_corpus(identifier, 8)
        self.cli("cache", "corpus-list", identifier, "--checkpoint", first["checkpoint"], ok=False)

    def test_metadata_discovery_does_not_audit_payloads_or_unselected_results(self):
        identifier, _ = self.create()
        self.run_corpus(identifier)
        first = self.listing(identifier, "--limit", "1")
        ref = first["items"][0]["components"]["summary"]["object"]
        objects = list((self.root / "data/owner/repo/cache/objects").glob(ref["sha256"] + "*"))
        self.assertEqual(len(objects), 1)
        objects[0].write_text("corrupt")
        self.listing(identifier, "--limit", "1")
        self.cli("cache", "corpus-status", identifier, ok=False)
        second = self.listing(identifier, "--offset", "1", "--checkpoint", first["checkpoint"])
        snapshot = self.root / "data/owner/repo/cache/snapshots" / (second["items"][0]["snapshot_id"] + ".json")
        snapshot.write_text("corrupt")
        self.listing(identifier, "--limit", "1")
        self.cli("cache", "corpus-list", identifier, "--offset", "1", "--checkpoint", first["checkpoint"], ok=False)

    def test_discovery_rejects_corrupt_state_and_omits_unbounded_error_text(self):
        from hashlib import sha256

        identifier, _ = self.create()
        self.run_corpus(identifier, 1)
        path = self.root / "data/owner/repo/cache/corpora" / (identifier + ".state.json")
        state = json.loads(path.read_text())
        state.pop("checksum")
        state["items"]["pr:1"]["error"] = "private error text" * 10000
        state["last_run"]["reason"] = "private reason text" * 10000
        state["checksum"] = sha256(json.dumps(state, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
        path.write_text(json.dumps(state))
        compact = json.loads(self.cli("cache", "corpus-progress", identifier).stdout)
        self.assertTrue(compact["reason_truncated"])
        self.assertEqual(len(compact["last_run"]["reason"]), 500)
        self.assertNotIn("private error text", json.dumps(compact))
        before = len(self.calls())
        result = self.listing(identifier)
        self.assertTrue(result["items"][0]["error_present"])
        self.assertTrue(result["last_run"]["reason_present"])
        self.assertNotIn("private", json.dumps(result))
        path.write_text("corrupt")
        self.cli("cache", "corpus-list", identifier, ok=False)
        self.assertEqual(len(self.calls()), before)

    def test_empty_corpus_discovery_has_no_continuation(self):
        identifier, _ = self.create([self.row("issue", 3)])
        result = self.listing(identifier)
        self.assertEqual(result["items"], [])
        self.assertEqual(result["pagination"]["total"], 0)
        self.assertIsNone(result["continuation"])

    def test_offline_creation_freezes_inventory_and_is_idempotent(self):
        identifier, snapshot = self.create([self.row("pr", 1), self.row("issue", 3)])
        before = len(self.calls())
        status = self.status(identifier)
        self.assertEqual(status["counts"]["pending"], 1)
        again = json.loads(self.cli("cache", "corpus-create", "--snapshot", snapshot, "--profile", "discussion").stdout)
        self.assertEqual(again["corpus_id"], identifier)
        self.assertEqual(len(self.calls()), before)
        self.fetch_inventory([self.row("pr", 2)])
        self.assertEqual([member["number"] for member in self.status(identifier)["plan"]["members"]], [1])
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())

    def test_one_budget_across_members_and_restart_skips_fresh_completed(self):
        identifier, _ = self.create()
        before = len(self.calls())
        result = self.run_corpus(identifier, 4)
        self.assertEqual(len(self.calls()) - before, 4)
        self.assertEqual(result["counts"], dict(pending=1, running=0, complete=1, gaps=0, error=0))
        self.assertEqual(result["progress"]["status"], "stopped")
        self.assertEqual(result["progress"]["last_run"]["requests"], 4)
        first_snapshot = result["progress"]["items"]["pr:1"]["snapshot_id"]
        resumed = self.run_corpus(identifier, 4)
        self.assertEqual(resumed["counts"]["complete"], 2)
        self.assertEqual(resumed["progress"]["status"], "finished")
        self.assertEqual(resumed["progress"]["items"]["pr:1"]["snapshot_id"], first_snapshot)
        before = len(self.calls())
        self.assertEqual(self.run_corpus(identifier)["progress"]["last_run"]["requests"], 0)
        self.assertEqual(len(self.calls()), before)

    def test_item_limit_stops_after_one_member_and_resume_continues(self):
        identifier, _ = self.create()
        self.seed_details()

        first = json.loads(self.cli("cache", "corpus-run", identifier, "--item-limit", "1", "--request-budget", "40", "--compact").stdout)
        self.assertEqual(first["declared_counts"]["complete"], 1)
        self.assertEqual(first["declared_counts"]["pending"], 1)
        self.assertEqual(first["last_run"]["reason"], "item limit reached")
        self.assertLess(first["last_run"]["requests"], 40)

        resumed = json.loads(self.cli("cache", "corpus-run", identifier, "--item-limit", "1", "--request-budget", "40", "--keep-complete", "--compact").stdout)
        self.assertEqual(resumed["declared_counts"]["complete"], 2)
        self.assertEqual(resumed["status"], "finished")

    def test_keep_complete_resumes_pending_after_one_day_without_refreshing_saved_items(self):
        identifier, _ = self.create()
        first = self.run_corpus(identifier, 4)
        saved = first["progress"]["items"]["pr:1"]
        before = len(self.calls())
        code = """
import sys, runpy
from datetime import datetime, timedelta, timezone
sys.path.insert(0, 'bin')
import _acquire, _reader, _corpus
future = (datetime.now(timezone.utc) + timedelta(days=2)).isoformat()
_acquire.now = _reader.now = _corpus.now = lambda: future
sys.argv = ['bin/cache', 'corpus-run', sys.argv[1], '--request-budget', '4', '--keep-complete']
runpy.run_path('bin/cache', run_name='__main__')
"""
        result = subprocess.run([sys.executable, "-c", code, identifier], cwd=self.root, env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        resumed = json.loads(result.stdout)
        self.assertEqual(resumed["progress"]["items"]["pr:1"], saved)
        self.assertEqual(resumed["progress"]["items"]["pr:2"]["outcome"], "complete")
        self.assertEqual(resumed["progress"]["status"], "finished")
        self.assertTrue(any("observation outside freshness window" in values for values in resumed["current_problems"]["pr:1"].values()))
        self.assertFalse(any("/pulls/1" in call[-1] or "/issues/1/" in call[-1] for call in self.calls()[before:]))

    def test_open_items_attempt_prs_before_issues(self):
        identifier, _ = self.create([self.row("issue", 1), self.row("pr", 2)], scope="open-items")
        self.seed_details()
        before = len(self.calls())
        result = json.loads(self.cli("cache", "corpus-run", identifier, "--request-budget", "4", "--compact").stdout)
        endpoints = [call[-1] for call in self.calls()[before:]]
        self.assertIn("repos/owner/repo/pulls/2", endpoints, result)
        self.assertFalse(any("repos/owner/repo/issues/1" in endpoint for endpoint in endpoints))
        self.assertEqual(result["declared_counts"]["pending"], 1)

    def test_keep_complete_attempts_pending_before_retrying_gaps(self):
        identifier, _ = self.create(profile="discussion")
        self.seed_details()
        endpoint = "repos/owner/repo/issues/1/comments?per_page=100&page=1"
        self.responses[endpoint] = dict(status=503)
        (self.mock / "responses.json").write_text(json.dumps(self.responses))
        first = json.loads(self.cli("cache", "corpus-run", identifier, "--request-budget", "3").stdout)
        self.assertEqual(first["counts"]["gaps"], 1)
        self.assertEqual(first["counts"]["pending"], 1)

        before = len(self.calls())
        resumed = json.loads(self.cli("cache", "corpus-run", identifier, "--request-budget", "4", "--keep-complete").stdout)
        endpoints = [call[-1] for call in self.calls()[before:]]
        self.assertIn("repos/owner/repo/pulls/2", endpoints)
        self.assertNotIn(endpoint, endpoints)
        self.assertEqual(resumed["counts"]["complete"], 1, (resumed["counts"], resumed["progress"]["last_run"], endpoints))
        self.assertEqual(resumed["progress"]["items"]["pr:1"]["attempts"], 1)

        self.responses[endpoint] = dict(data=[])
        (self.mock / "responses.json").write_text(json.dumps(self.responses))
        repaired = json.loads(self.cli("cache", "corpus-run", identifier, "--request-budget", "4", "--keep-complete").stdout)
        self.assertEqual(repaired["counts"]["complete"], 2)
        self.assertEqual(repaired["progress"]["items"]["pr:1"]["attempts"], 2)

    def test_budget_inside_item_records_gaps_not_completion(self):
        identifier, _ = self.create()
        result = self.run_corpus(identifier, 2)
        self.assertEqual(result["counts"]["gaps"], 1)
        self.assertEqual(result["counts"]["pending"], 1)
        self.assertEqual(result["progress"]["last_run"]["requests"], 2)
        self.assertTrue(result["current_problems"]["pr:1"]["summary"])
        resumed = self.run_corpus(identifier, 8)
        self.assertEqual(resumed["counts"]["complete"], 2)

    def test_budget_before_item_summary_is_error_and_resume_is_safe(self):
        identifier, _ = self.create()
        result = self.run_corpus(identifier, 1)
        self.assertEqual(result["counts"]["error"], 1)
        self.assertEqual(result["progress"]["status"], "stopped")
        self.assertEqual(self.run_corpus(identifier, 8)["counts"]["complete"], 2)

    def test_offline_status_and_empty_selection(self):
        identifier, _ = self.create([self.row("issue", 3)])
        before = len(self.calls())
        result = self.run_corpus(identifier)
        self.assertEqual(result["progress"]["status"], "finished")
        self.assertEqual(sum(result["counts"].values()), 0)
        self.status(identifier)
        self.assertEqual(len(self.calls()), before)

    def test_detail_snapshots_are_not_inventories(self):
        result = self.run_cache("fetch", "pr", "discussion")
        self.cli("cache", "corpus-create", "--snapshot", result["snapshot_id"], ok=False)

    def test_all_items_scope_and_profile_are_frozen(self):
        identifier, _ = self.create([self.row("pr", 1), self.row("issue", 3)], scope="open-items", profile="pr-context")
        result = self.status(identifier)
        self.assertEqual(result["plan"]["profile"], "pr-context")
        self.assertEqual([member["kind"] for member in result["plan"]["members"]], ["issue", "pr"])
        self.assertEqual(result["counts"]["pending"], 2)

    def test_corrupt_plan_progress_and_missing_evidence_are_refused(self):
        identifier, _ = self.create()
        self.run_corpus(identifier, 4)
        root = self.root / "data/owner/repo/cache"
        state = root / "corpora" / (identifier + ".state.json")
        saved = state.read_bytes()
        state.write_text("{}")
        before = len(self.calls())
        self.cli("cache", "corpus-run", identifier, ok=False)
        self.cli("cache", "corpus-status", identifier, ok=False)
        self.assertEqual(len(self.calls()), before)
        state.write_bytes(saved)
        entry = self.status(identifier)["progress"]["items"]["pr:1"]
        (root / "snapshots" / (entry["snapshot_id"] + ".json")).unlink()
        self.cli("cache", "corpus-status", identifier, ok=False)
        plan = root / "corpora" / (identifier + ".plan.json")
        plan.write_text("{}")
        self.cli("cache", "corpus-run", identifier, ok=False)
        self.cli("cache", "corpus-list", identifier, ok=False)

    def test_keyboard_interrupt_retains_resumable_state(self):
        identifier, _ = self.create()
        code = """
import sys
sys.path.insert(0, 'bin')
import _corpus
from _cache import EvidenceCache
from _evidence import repository
def interrupt(*args, **kwargs):
    raise KeyboardInterrupt()
_corpus.acquire = interrupt
_corpus.run(EvidenceCache(repository('owner/repo')), sys.argv[1])
"""
        result = subprocess.run([sys.executable, "-c", code, identifier], cwd=self.root, env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(self.status(identifier)["progress"]["status"], "interrupted")
        self.assertEqual(self.run_corpus(identifier, 8)["counts"]["complete"], 2)

    def test_kill_after_evidence_publication_resumes_without_refetch(self):
        identifier, _ = self.create()
        self.seed_details()
        code = """
import os, sys
sys.path.insert(0, 'bin')
import _corpus
from _cache import EvidenceCache
from _evidence import repository
original = _corpus.acquire
def acquire(*args, **kwargs):
    original(*args, **kwargs)
    os._exit(73)
_corpus.acquire = acquire
_corpus.run(EvidenceCache(repository('owner/repo')), sys.argv[1])
"""
        result = subprocess.run([sys.executable, "-c", code, identifier], cwd=self.root, env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 73)
        self.assertEqual(self.status(identifier)["counts"]["running"], 1)
        before = len(self.calls())
        resumed = self.run_corpus(identifier, 4)
        self.assertEqual(resumed["counts"]["complete"], 2)
        self.assertEqual(len(self.calls()) - before, 4)

    def test_throttle_persists_across_corpus_invocations(self):
        identifier, _ = self.create()
        self.seed_details()
        self.responses["repos/owner/repo"] = dict(status=429, headers={"Retry-After": "120"})
        (self.mock / "responses.json").write_text(json.dumps(self.responses))
        stopped = json.loads(self.cli("cache", "corpus-run", identifier).stdout)
        self.assertEqual(stopped["progress"]["status"], "stopped")
        before = len(self.calls())
        stopped = self.run_corpus(identifier)
        self.assertEqual(stopped["progress"]["last_run"]["requests"], 0)
        self.assertEqual(len(self.calls()), before)

    def test_newer_inventory_forces_revisit_without_changing_membership(self):
        identifier, _ = self.create([self.row("pr", 1)])
        self.run_corpus(identifier, 4)
        self.fetch_inventory([self.row("pr", 1), self.row("pr", 2)])
        before = len(self.calls())
        result = self.run_corpus(identifier, 4)
        self.assertEqual(len(self.calls()) - before, 3)
        self.assertFalse(any("/comments?" in call[-1] for call in self.calls()[before:]))
        self.assertEqual(result["counts"]["complete"], 1)
        self.assertEqual(result["progress"]["items"]["pr:1"]["attempts"], 2)
        self.assertNotIn("pr:2", result["progress"]["items"])

    def test_finished_pass_can_have_gaps_and_later_retry_repairs_them(self):
        identifier, _ = self.create([self.row("pr", 1)])
        self.seed_details()
        self.responses["repos/owner/repo/issues/1/comments?per_page=100&page=1"] = dict(status=503)
        (self.mock / "responses.json").write_text(json.dumps(self.responses))
        result = json.loads(self.cli("cache", "corpus-run", identifier).stdout)
        self.assertEqual(result["progress"]["status"], "finished")
        self.assertEqual(result["counts"]["gaps"], 1)
        self.assertTrue(result["current_problems"]["pr:1"]["comments"])
        self.assertEqual(self.run_corpus(identifier)["counts"]["complete"], 1)

    def test_closed_member_remains_in_frozen_selection(self):
        identifier, _ = self.create([self.row("pr", 1)])
        self.seed_details()
        self.responses["repos/owner/repo/pulls/1"]["data"].update(state="closed", merged=True)
        (self.mock / "responses.json").write_text(json.dumps(self.responses))
        result = json.loads(self.cli("cache", "corpus-run", identifier).stdout)
        self.assertEqual(result["counts"]["complete"], 1)
        snapshot = result["progress"]["items"]["pr:1"]["snapshot_id"]
        self.assertEqual(self.run_cache("read", "pr", "discussion", "--snapshot", snapshot)["data"]["summary"]["state"], "merged")

    def test_concurrent_runners_do_not_duplicate_fresh_work(self):
        identifier, _ = self.create()
        self.seed_details()
        before = len(self.calls())
        args = [str(self.root / "bin/cache"), "corpus-run", identifier]
        first = subprocess.Popen(args, cwd=self.root, env=self.env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        second = subprocess.Popen(args, cwd=self.root, env=self.env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
        for process in (first, second):
            out, err = process.communicate(timeout=20)
            self.assertEqual(process.returncode, 0, err)
            self.assertEqual(json.loads(out)["counts"]["complete"], 2)

        self.assertEqual(len(self.calls()) - before, 8)

    def test_resealed_invalid_state_and_symlink_are_refused(self):
        identifier, _ = self.create()
        self.run_corpus(identifier, 4)
        path = self.root / "data/owner/repo/cache/corpora" / (identifier + ".state.json")
        original = json.loads(path.read_text())
        for edit in (lambda s: s.update(schema_version=2), lambda s: s.update(plan_id="0" * 64), lambda s: s["items"].pop("pr:2"), lambda s: s["items"]["pr:2"].update(outcome="complete"), lambda s: s["last_run"].update(requests=500)):
            state = copy.deepcopy(original)
            edit(state)
            from hashlib import sha256
            content = {key: value for key, value in state.items() if key != "checksum"}
            state["checksum"] = sha256(json.dumps(content, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
            path.write_text(json.dumps(state))
            self.cli("cache", "corpus-status", identifier, ok=False)
            self.cli("cache", "corpus-list", identifier, ok=False)

        path.unlink()
        target = self.root / "preserved.json"
        target.write_text(json.dumps(original))
        path.symlink_to(target)
        before = target.read_bytes()
        self.cli("cache", "corpus-run", identifier, ok=False)
        self.cli("cache", "corpus-list", identifier, ok=False)
        self.assertEqual(target.read_bytes(), before)

    def test_incremental_snapshot_cannot_claim_whole_open_corpus(self):
        _, _ = self.create([self.row("pr", 1)])
        self.cli("sync")
        from datetime import datetime, timedelta
        since = (datetime.strptime(self.meta()["synced_through"], "%Y-%m-%dT%H:%M:%SZ") - timedelta(minutes=5)).strftime("%Y-%m-%dT%H:%M:%SZ")
        self.responses[f"repos/owner/repo/issues?state=all&since={since}&per_page=100&page=1"] = dict(data=[self.row("pr", 1)])
        self.fetch_inventory()
        snapshot = json.loads(self.cli("cache", "import-inventory").stdout)["snapshot_id"]
        self.cli("cache", "corpus-create", "--snapshot", snapshot, ok=False)
