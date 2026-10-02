"""Evidence is acquired explicitly, then read from pinned local snapshots."""

import json
import subprocess
import sys

from support import Workspace, repository, summary


class EvidenceTests(Workspace):
    def setUp(self):
        super().setUp()
        self.seed_pr()

    def cache(self, command, *args):
        return self.json_cli("cache", command, *args)

    def inventory_row(self, number=1):
        row = summary(number=number, id=1000 + number, node_id=f"LIST_{number}")
        row.update(user=dict(login="author"), labels=[], created_at=row["updated_at"], pull_request=dict(url=f"https://api.github.com/repos/owner/repo/pulls/{number}"))
        return row

    def import_inventory(self, rows=None):
        self.responses["repos/owner/repo/issues?state=open&per_page=100&page=1"] = dict(data=rows if rows is not None else [self.inventory_row()])
        self.run_cli("fetch", "--cache-inventory")
        return self.cache("import-inventory")["snapshot_id"]

    def test_missing_offline_read_does_not_fetch_or_initialize(self):
        result = self.cache("read", "--kind", "pr", "--number", "1", "--profile", "discussion")
        self.assertIsNone(result["snapshot_id"])
        self.assertEqual(result["problems"]["summary"], ["missing"])
        self.assertEqual(self.calls(), [])
        self.assertFalse((self.root / "data/owner/repo/cache").exists())

    def test_selected_acquisition_and_offline_reuse(self):
        first = self.cache("fetch", "--kind", "pr", "--number", "1", "--profile", "discussion")
        self.assertFalse(any(first["problems"].values()))
        count = len(self.calls())
        fixed = self.cache("read", "--kind", "pr", "--number", "1", "--profile", "discussion", "--snapshot", first["snapshot_id"])
        self.assertEqual(fixed["data"]["summary"]["body"], "Useful source text")
        self.assertEqual(len(self.calls()), count)
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())

    def test_empty_graphql_cli_response_uses_bounded_budgeted_retry(self):
        (self.mock / "gh").write_text('''#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
root = Path(os.environ["FAKE_GH_DIR"])
args = sys.argv[1:]
if args[:1] != ["api"] or args[args.index("--method") + 1] != "POST" or args[-1] != "graphql":
    sys.exit("unexpected GitHub operation")
payload = json.load(sys.stdin)
if not payload["query"].startswith("query ClosingIssues"):
    sys.exit("unexpected GraphQL query")
counter = root / "graphql-count"
count = int(counter.read_text()) + 1 if counter.exists() else 1
counter.write_text(str(count))
if (root / "gateway-timeout").exists() and count < 3:
    print("HTTP/2.0 504")
    print()
    sys.exit(1)
print("HTTP/2.0 200")
if (root / "rate-limited").exists():
    print("x-ratelimit-remaining: 0")
print()
if count < 3:
    sys.exit("transient gh transport failure")
print(json.dumps({"data": {"rateLimit": {"remaining": 100}, "node": {"id": "PR_1"}}}))
''')
        code = '''import json
from _acquire import GitHubReader, ReadFailure
reader = GitHubReader("github.com", budget=BUDGET)
try:
    node = reader.closing_page("PR_1", None)
    print(json.dumps({"requests": reader.requests, "node": node}))
except ReadFailure as error:
    print(json.dumps({"requests": reader.requests, "error": str(error)}))
'''
        env = dict(self.env, PYTHONPATH=str(self.root / "bin"))
        first = subprocess.run([sys.executable, "-c", code.replace("BUDGET", "5")], cwd=self.root, env=env,
                               capture_output=True, text=True, check=True)
        self.assertEqual(json.loads(first.stdout), dict(requests=3, node=dict(id="PR_1")))

        (self.mock / "graphql-count").unlink()
        limited = subprocess.run([sys.executable, "-c", code.replace("BUDGET", "2")], cwd=self.root, env=env,
                                 capture_output=True, text=True, check=True)
        failure = json.loads(limited.stdout)
        self.assertEqual(failure["requests"], 2)
        self.assertIn("empty GraphQL response", failure["error"])
        self.assertNotIn("invalid JSON", failure["error"])

        (self.mock / "graphql-count").unlink()
        (self.mock / "gateway-timeout").touch()
        retried = subprocess.run([sys.executable, "-c", code.replace("BUDGET", "5")], cwd=self.root, env=env,
                                 capture_output=True, text=True, check=True)
        self.assertEqual(json.loads(retried.stdout), dict(requests=3, node=dict(id="PR_1")))

        (self.mock / "graphql-count").unlink()
        exhausted = subprocess.run([sys.executable, "-c", code.replace("BUDGET", "2")], cwd=self.root, env=env,
                                   capture_output=True, text=True, check=True)
        self.assertEqual(json.loads(exhausted.stdout)["requests"], 2)
        self.assertIn("HTTP 504", json.loads(exhausted.stdout)["error"])

        (self.mock / "graphql-count").unlink()
        (self.mock / "gateway-timeout").unlink()
        (self.mock / "rate-limited").touch()
        throttled = subprocess.run([sys.executable, "-c", code.replace("BUDGET", "5")], cwd=self.root, env=env,
                                   capture_output=True, text=True, check=True)
        self.assertEqual(json.loads(throttled.stdout)["requests"], 1)
        self.assertIn("rate limit", json.loads(throttled.stdout)["error"])

    def test_git_batch_transfer_retries_transient_failure_within_budget(self):
        (self.mock / "git").write_text('''#!/usr/bin/env python3
import os, sys
from pathlib import Path
root = Path(os.environ["FAKE_GH_DIR"])
args = sys.argv[1:]
if args[:1] == ["-c"]:
    if args[:4] != ["-c", "credential.helper=", "-c", "credential.helper=!gh auth git-credential"]:
        sys.exit("unexpected Git credential helper")
    args = args[4:]
if args[:1] != ["-C"]:
    sys.exit("unexpected Git operation")
if args[2:] == ["rev-parse", "--show-toplevel"]:
    print(root.parent.parent)
elif args[2:] == ["check-ref-format", "refs/heads/main"]:
    pass
elif args[2] == "fetch":
    if "refs/pull/1/head" not in args or "refs/heads/main" not in args:
        sys.exit("PR head or base ref was not fetched")
    counter = root / "git-count"
    count = int(counter.read_text()) + 1 if counter.exists() else 1
    counter.write_text(str(count))
    if (root / "git-hard").exists():
        sys.exit("Repository not found")
    if count < 3:
        sys.exit("RPC failed; transient connection loss")
else:
    sys.exit("unexpected Git operation")
''')
        (self.mock / "git").chmod(0o755)
        code = '''import json
from types import SimpleNamespace
from _acquire import ReadFailure
from _bulk import git_heads
reader = SimpleNamespace(requests=0, budget=5, stopped=None)
cache = SimpleNamespace(identity={"host": "github.com", "full_name": "owner/repo"})
try:
    git_heads(reader, cache, [{"number": 1}], {1: {"baseRefName": "main"}})
    print(json.dumps({"requests": reader.requests, "status": "complete"}))
except ReadFailure as error:
    print(json.dumps({"requests": reader.requests, "error": str(error)}))
'''
        env = dict(self.env, PYTHONPATH=str(self.root / "bin"))
        result = subprocess.run([sys.executable, "-c", code], cwd=self.root, env=env, capture_output=True, text=True, check=True)
        self.assertEqual(json.loads(result.stdout), dict(requests=3, status="complete"))

        (self.mock / "git-count").unlink()
        (self.mock / "git-hard").touch()
        hard = subprocess.run([sys.executable, "-c", code], cwd=self.root, env=env, capture_output=True, text=True, check=True)
        self.assertEqual(json.loads(hard.stdout)["requests"], 1)
        self.assertIn("repository access", json.loads(hard.stdout)["error"])

    def test_partial_latest_observation_remains_visible(self):
        first = self.cache("fetch", "--kind", "pr", "--number", "1", "--profile", "discussion")
        self.responses["repos/owner/repo/pulls/1"] = dict(data=summary(head=dict(sha="c" * 40, repo=repository())))
        partial = self.cache("fetch", "--kind", "pr", "--number", "1", "--profile", "discussion", "--mode", "refresh", "--request-budget", "2")
        self.assertNotEqual(partial["snapshot_id"], first["snapshot_id"])
        latest = self.cache("read", "--kind", "pr", "--number", "1", "--profile", "discussion")
        self.assertEqual(latest["snapshot_id"], partial["snapshot_id"])
        self.assertTrue(any(latest["problems"].values()))
        old = self.cache("read", "--kind", "pr", "--number", "1", "--profile", "discussion", "--snapshot", first["snapshot_id"])
        self.assertFalse(any(old["problems"].values()))

    def test_inventory_summary_is_partial_and_separate_from_ledger(self):
        snapshot = self.import_inventory()
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())
        before = len(self.calls())
        record = self.cache("read", "--kind", "pr", "--number", "1", "--profile", "discussion", "--snapshot", snapshot)
        self.assertEqual(record["problems"]["summary"], ["partial"])
        self.assertEqual(record["problems"]["comments"], ["missing"])
        self.assertIsNone(record["identity"]["database_id"])
        self.assertEqual(len(self.calls()), before)

        self.run_cli("sync")
        row = self.ledger()[("pr", 1)]
        self.assertNotIn("body", row)
        self.assertFalse(row["reviewed"])

    def test_frozen_search_stays_offline(self):
        snapshot = self.import_inventory()
        before = len(self.calls())
        result = self.cache("search", "--snapshot", snapshot, "--query", "Useful", "--component", "summary")
        self.assertEqual(result["matched_items"], 1)
        self.assertEqual(result["items"][0]["match"]["pointer"], "/body")
        self.assertEqual(len(self.calls()), before)

    def test_corpus_membership_is_frozen(self):
        snapshot = self.import_inventory()
        corpus = self.cache("corpus-create", "--snapshot", snapshot, "--scope", "open-prs", "--profile", "discussion")["corpus_id"]
        self.import_inventory([self.inventory_row(1), self.inventory_row(2)])
        before = len(self.calls())
        status = self.cache("corpus-status", corpus)
        self.assertEqual(status["plan"]["inventory_snapshot"], snapshot)
        self.assertEqual(len(status["plan"]["members"]), 1)
        self.assertEqual(len(self.calls()), before)

    def test_corpus_run_publishes_pinned_item_evidence(self):
        snapshot = self.import_inventory()
        corpus = self.cache("corpus-create", "--snapshot", snapshot, "--scope", "open-prs", "--profile", "discussion")["corpus_id"]
        interrupted = self.cache("corpus-run", corpus, "--request-budget", "1")
        self.assertEqual(interrupted["counts"]["complete"], 0)
        result = self.cache("corpus-run", corpus, "--request-budget", "100")
        self.assertEqual(result["counts"]["complete"], 1)
        before = len(self.calls())
        listing = self.cache("corpus-list", corpus)
        self.assertEqual(listing["items"][0]["identity"]["number"], 1)
        self.assertEqual(len(self.calls()), before)

    def test_corrupt_selected_payload_is_never_read_as_evidence(self):
        result = self.cache("fetch", "--kind", "pr", "--number", "1", "--profile", "discussion")
        manifest = self.cache("show", result["snapshot_id"])
        reference = manifest["items"][0]["components"]["summary"]["object"]
        object_path = next((self.root / "data/owner/repo/cache/objects").glob(reference["sha256"] + "*"))
        object_path.write_text("damaged")
        before = len(self.calls())
        self.run_cli("cache", "read", "--kind", "pr", "--number", "1", "--profile", "discussion", ok=False)
        self.assertEqual(len(self.calls()), before)
