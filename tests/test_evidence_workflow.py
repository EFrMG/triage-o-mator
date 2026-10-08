"""Evidence is acquired explicitly, then read from pinned local snapshots."""

import json
import subprocess
import sys

from support import Workspace, item, repository, summary


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

    def test_duplicate_comparison_keeps_unresolved_pair_and_both_selected_revisions(self):
        rows = []
        snapshots = {}
        for number in (2, 3):
            issue = summary(kind="issue", number=number, title=f"Related report {number}")
            self.responses[f"repos/owner/repo/issues/{number}"] = dict(data=issue)
            self.responses[f"repos/owner/repo/issues/{number}/comments?per_page=100&page=1"] = dict(data=[])
            row = item(number, issue["title"])
            row["updated_at"] = issue["updated_at"]
            rows.append(row)

        ledger = self.root / "data/owner/repo/ledger.jsonl"
        ledger.write_text("\n".join(json.dumps(row) for row in rows) + "\n")
        for number in (2, 3):
            snapshots[number] = self.cache("fetch", "--kind", "issue", "--number", str(number), "--profile", "discussion")["snapshot_id"]

        calls = len(self.calls())
        args = ("--expected-repo", "owner/repo", "record", "--evidence", f"issue:2:{snapshots[2]}",
                "--evidence", f"issue:3:{snapshots[3]}", "--outcome", "unresolved", "--confidence", "low",
                "--shared", "Both describe the same screen", "--difference", "The trigger is not established",
                "--gap", "Need a reproduction from #3", "--by", "agent:tester")
        saved = self.json_cli("duplicate-assessment", *args)
        self.assertEqual(saved["pair"], dict(kind="issue", a=2, b=3))
        self.assertEqual(saved["outcome"], "unresolved")
        self.assertEqual(self.json_cli("duplicate-assessment", "--expected-repo", "owner/repo", "list")["records"][0]["assessment_id"], saved["assessment_id"])
        self.assertEqual([row["snapshot_id"] for row in saved["evidence"]], [snapshots[2], snapshots[3]])
        self.assertEqual(self.json_cli("duplicate-assessment", *args), saved)
        shown = self.json_cli("duplicate-assessment", "--expected-repo", "owner/repo", "show", saved["assessment_id"])
        self.assertTrue(all(row["current"] for row in shown["current"]))
        self.assertEqual(len(self.calls()), calls)

        grouped = self.json_cli("group-assessment", "--expected-repo", "owner/repo", "record", "--scope-type", "topic",
                                "--topic", "Related screen reports", "--evidence", f"issue:2:{snapshots[2]}",
                                "--evidence", f"issue:3:{snapshots[3]}", "--outcome", "no-group",
                                "--reason", "Trigger overlap is too uncertain for a maintainer group", "--by", "agent:tester")
        self.assertIsNone(grouped["group"])
        self.assertEqual(grouped["scope"]["member_keys"], ["issue:2", "issue:3"])
        self.assertEqual(self.json_cli("group-assessment", "--expected-repo", "owner/repo", "list")["records"][0]["assessment_id"], grouped["assessment_id"])
        self.assertEqual(len(self.calls()), calls)

        rows[1]["updated_at"] = "2026-10-07T00:00:00Z"
        ledger.write_text("\n".join(json.dumps(row) for row in rows) + "\n")
        changed = self.json_cli("duplicate-assessment", "--expected-repo", "owner/repo", "show", saved["assessment_id"])
        self.assertEqual([row["current"] for row in changed["current"]], [True, False])
        group_status = self.json_cli("group-assessment", "--expected-repo", "owner/repo", "show", grouped["assessment_id"])
        self.assertEqual([row["ledger_revision_current"] for row in group_status["current"]["sources"]], [True, False])
        self.assertIn("high confidence requires", self.run_cli("duplicate-assessment", *args[:args.index("--confidence")],
                                                                  "--confidence", "high", *args[args.index("--shared"):], ok=False).stderr)

        self.json_cli("not-duplicate", "--key", "issue:2", "--key", "issue:3", "--by", "maintainer", "--note", "Different triggers")
        duplicate = list(args)
        duplicate[duplicate.index("unresolved")] = "duplicate"
        self.assertIn("not-duplicate verdict", self.run_cli("duplicate-assessment", *duplicate, ok=False).stderr)

    def test_pr_review_and_keep_open_assessments_bind_head_and_coverage(self):
        pr = item(1, "A contribution", kind="pr")
        pr["updated_at"] = summary()["updated_at"]
        ledger = self.root / "data/owner/repo/ledger.jsonl"
        ledger.write_text(json.dumps(pr) + "\n")
        selected = self.cache("fetch", "--kind", "pr", "--number", "1", "--profile", "discussion")
        calls = len(self.calls())
        common = ("--expected-repo", "owner/repo", "record", "--number", "1", "--snapshot", selected["snapshot_id"],
                  "--code-read", "partial", "--base-comparison", "unavailable", "--by", "agent:tester")
        review = self.json_cli("pr-assessment", *common, "--purpose", "review", "--outcome", "no-finding",
                               "--reason", "No issue found in the available discussion and summary")
        self.assertEqual(review["evidence"]["revision"]["head_sha"], "b" * 40)
        self.assertIn("diff is not complete", review["gaps"])
        self.assertEqual(review["code"]["read"], "partial")
        self.assertEqual(self.json_cli("pr-assessment", *common, "--purpose", "closure", "--outcome", "keep-open",
                                       "--reason", "Code and feedback need review")["outcome"], "keep-open")
        self.assertIn("ready reviews require", self.run_cli("pr-assessment", *common, "--purpose", "review", "--outcome", "ready",
                                                               "--reason", "Merge now", ok=False).stderr)
        self.assertIn("needs a pending exact closure proposal", self.run_cli("pr-assessment", *common, "--purpose", "closure",
                                                                            "--outcome", "close", "--reason", "Superseded", ok=False).stderr)
        shown = self.json_cli("pr-assessment", "--expected-repo", "owner/repo", "show", review["assessment_id"])
        self.assertTrue(shown["current"]["ledger_revision_current"])
        self.assertEqual(len(self.calls()), calls)

        pr["updated_at"] = "2026-10-07T00:00:00Z"
        ledger.write_text(json.dumps(pr) + "\n")
        self.assertFalse(self.json_cli("pr-assessment", "--expected-repo", "owner/repo", "show", review["assessment_id"])["current"]["ledger_revision_current"])

    def test_item_score_binds_numeric_assessment_to_verified_revision(self):
        issue = summary(kind="issue", number=2)
        self.responses["repos/owner/repo/issues/2"] = dict(data=issue)
        self.responses["repos/owner/repo/issues/2/comments?per_page=100&page=1"] = dict(data=[])
        row = item(2, issue["title"])
        row["updated_at"] = issue["updated_at"]
        row.update(action="", confidence="")
        (self.root / "data/owner/repo/ledger.jsonl").write_text(json.dumps(row) + "\n")

        selected = self.cache("fetch", "--kind", "issue", "--number", "2", "--profile", "discussion")
        calls = len(self.calls())
        args = ("--expected-repo", "owner/repo", "set", "--kind", "issue", "--number", "2")
        unclear = self.run_cli("item-score", *args, "--snapshot", selected["snapshot_id"], "--clarity", "2", "--support", "1",
                               "--actionability", "1", "--reason", "The case looks good", "--by", "agent:tester", ok=False)
        self.assertIn("numeric reason must start with Clarity 2:", unclear.stderr)
        self.assertIsNone(self.ledger()["issue", 2].get("item_score"))
        scored = self.json_cli("item-score", *args, "--snapshot", selected["snapshot_id"], "--clarity", "2", "--support", "1",
                               "--actionability", "1", "--reason", "Clarity 2: Goal is clear. Support 1: One example is present. Actionability 1: Check the named case.", "--by", "agent:tester")
        self.assertEqual(scored["score"]["value"], 4)
        self.assertEqual(scored["score"]["dimensions"], dict(clarity=2, support=1, actionability=1))
        self.assertEqual(self.json_cli("item-score", "--expected-repo", "owner/repo", "show", "--kind", "issue", "--number", "2")["current"], True)
        self.assertEqual(len(self.calls()), calls)
        self.assertIsNone(self.ledger()["issue", 2]["review_request"])

        changed = self.ledger()["issue", 2]
        changed["updated_at"] = "2026-09-23T01:00:00Z"
        (self.root / "data/owner/repo/ledger.jsonl").write_text(json.dumps(changed) + "\n")
        self.assertTrue(self.json_cli("item-score", "--expected-repo", "owner/repo", "show", "--kind", "issue", "--number", "2")["current"])
        changed["comments_count"] += 1
        (self.root / "data/owner/repo/ledger.jsonl").write_text(json.dumps(changed) + "\n")
        self.assertFalse(self.json_cli("item-score", "--expected-repo", "owner/repo", "show", "--kind", "issue", "--number", "2")["current"])

        rejected = self.run_cli("item-score", *args, "--snapshot", selected["snapshot_id"], "--clarity", "2", "--support", "2",
                                "--actionability", "1", "--reason", "Clarity 2: Goal is clear. Support 2: Reproduction is complete. Actionability 1: Check it.", "--by", "agent:tester", ok=False)
        self.assertIn("selected evidence revision differs", rejected.stderr)
        self.assertEqual(self.ledger()["issue", 2]["item_score"]["value"], 4)

        self.json_cli("item-score", *args, "--unassessed", "--reason", "Report needs fresh evidence", "--by", "agent:tester")
        shown = self.json_cli("item-score", "--expected-repo", "owner/repo", "show", "--kind", "issue", "--number", "2")
        self.assertIsNone(shown["score"]["value"])
        self.assertFalse(shown["current"])
        self.assertTrue(shown["assessment_current"])
        self.assertEqual(shown["score"]["revision"]["updated_at"], "2026-09-23T01:00:00Z")
        self.assertEqual(len(self.calls()), calls)

        changed = self.ledger()["issue", 2]
        changed.update(updated_at="2026-09-24T01:00:00Z", comments_count=changed["comments_count"] + 1)
        (self.root / "data/owner/repo/ledger.jsonl").write_text(json.dumps(changed) + "\n")
        self.assertFalse(self.json_cli("item-score", "--expected-repo", "owner/repo", "show", "--kind", "issue", "--number", "2")["assessment_current"])
        self.assertTrue(any("Score a bounded selection of 1" in row["what"] for row in self.json_cli("next", "--json")["suggestions"]))

        pr = item(1, "A contribution", kind="pr")
        pr["updated_at"] = summary()["updated_at"]
        (self.root / "data/owner/repo/ledger.jsonl").write_text(json.dumps(changed) + "\n" + json.dumps(pr) + "\n")
        partial = self.cache("fetch", "--kind", "pr", "--number", "1", "--profile", "discussion")
        incomplete = self.run_cli("item-score", "--expected-repo", "owner/repo", "set", "--kind", "pr", "--number", "1",
                                  "--snapshot", partial["snapshot_id"], "--correctness", "1", "--safeguards", "1", "--reviewability", "1",
                                  "--reason", "Correctness 1: A material question remains. Safeguards 1: A partial check exists. Reviewability 1: Scope is focused.",
                                  "--by", "agent:tester", ok=False)
        self.assertIn("complete files, diff", incomplete.stderr)
        self.assertIsNone(self.ledger()["pr", 1].get("item_score"))
        self.json_cli("item-score", "--expected-repo", "owner/repo", "set", "--kind", "pr", "--number", "1",
                      "--snapshot", partial["snapshot_id"], "--unassessed", "--reason", "PR code evidence is incomplete", "--by", "agent:tester")
        partial_score = self.json_cli("item-score", "--expected-repo", "owner/repo", "show", "--kind", "pr", "--number", "1")
        self.assertTrue(partial_score["assessment_current"])
        self.assertEqual(partial_score["score"]["snapshot_id"], partial["snapshot_id"])
        self.assertEqual(partial_score["score"]["revision"]["head_sha"], "b" * 40)
        self.assertNotEqual(partial_score["score"]["coverage"].get("diff"), "complete")

        # A refresh that sees the scored PR moved reads its head once: an unchanged head keeps the result current, a new one makes it stale.
        listed = dict(self.ledger()["pr", 1], updated_at="2026-09-25T00:00:00Z")
        self.responses["repos/owner/repo/issues?state=open&per_page=100"] = dict(data=[{key: listed[key] for key in ("number", "kind", "title", "url", "author", "created_at", "updated_at", "state", "state_reason", "labels", "comments_count")}])
        self.run_cli("fetch", "--full")
        self.run_cli("sync")
        self.assertEqual((self.ledger()["pr", 1]["updated_at"], self.ledger()["pr", 1]["head_sha"]), ("2026-09-25T00:00:00Z", "b" * 40))
        self.assertTrue(self.json_cli("item-score", "--expected-repo", "owner/repo", "show", "--kind", "pr", "--number", "1")["assessment_current"])
        self.responses["repos/owner/repo/pulls/1"]["data"]["head"]["sha"] = "c" * 40
        self.responses["repos/owner/repo/issues?state=open&per_page=100"]["data"][0]["updated_at"] = "2026-09-26T00:00:00Z"
        self.run_cli("fetch", "--full")
        self.run_cli("sync")
        self.assertFalse(self.json_cli("item-score", "--expected-repo", "owner/repo", "show", "--kind", "pr", "--number", "1")["assessment_current"])

        wrong_repo = self.run_cli("item-score", "--expected-repo", "other/repo", "show", "--kind", "pr", "--number", "1", ok=False)
        self.assertIn("selected repository changed", wrong_repo.stderr)

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
        self.assertIsNone(row["review_request"])

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
