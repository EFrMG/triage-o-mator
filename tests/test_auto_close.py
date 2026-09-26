"""Auto-close proposals remain offline until an exact human approval, and interrupted writes are not repeated."""

import json
import subprocess

from support import CheckoutTest


class AutoCloseTests(CheckoutTest):
    def setUp(self):
        super().setUp()
        (self.mock / "gh").write_text('''#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
root = Path(os.environ["FAKE_GH_DIR"])
args = sys.argv[1:]
method = args[args.index("--method") + 1]
endpoint = args[-1] if method == "GET" else args[-3]
with (root / "calls").open("a") as out:
    out.write(json.dumps([method, endpoint]) + "\\n")
item = json.loads((root / "item.json").read_text())
if method == "GET":
    if endpoint.endswith("/pulls/1"):
        print(json.dumps(dict(number=1, html_url=item["html_url"], title="Old approach", state=item["state"], merged=False, updated_at="2026-09-24T00:00:00Z", head=dict(sha=(root / "head").read_text()))))
    else:
        print(json.dumps(item))
elif method == "POST":
    print(json.dumps(dict(id=42, html_url=item["html_url"] + "#issuecomment-42")))
else:
    if (root / "fail-patch").exists():
        sys.exit("connection lost")
    item["state"] = "closed"
    print(json.dumps(item))
''')
        (self.mock / "gh").chmod(0o755)
        self.item = dict(number=1, html_url="https://github.com/owner/repo/pull/1", state="open", updated_at="2026-09-25T00:00:00Z", pull_request={})
        (self.mock / "item.json").write_text(json.dumps(self.item))
        (self.mock / "head").write_text("b" * 40)
        self.comment = self.root / "proposal.md"
        self.comment.write_text("This PR has been superseded by #2. Thank you for the work.")

    def call(self, *args, ok=True):
        result = subprocess.run([str(self.root / "bin/auto-close"), "--expected-repo", "owner/repo", *args], env=self.env, cwd=self.root, text=True, capture_output=True)
        self.assertEqual(result.returncode == 0, ok, result.stdout + result.stderr)
        return json.loads(result.stdout) if result.stdout and ok else result.stderr

    def propose(self):
        return self.call("propose", "--number", "1", "--title", "Old approach", "--head-sha", "b" * 40,
                         "--updated-at", self.item["updated_at"], "--rationale", "#2 replaces this work", "--comment-file", str(self.comment),
                         "--reference-kind", "pr", "--reference-number", "2", "--by", "agent:reviewer")

    def calls(self):
        path = self.mock / "calls"
        return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []

    def test_proposal_review_view_and_dismiss_are_offline(self):
        proposal = self.propose()
        self.assertEqual(self.calls(), [])
        self.assertTrue(proposal["needs_attention"])
        self.assertEqual(self.call("list")["rows"][0]["reference"], {"kind": "pr", "number": 2})
        review = self.call("review", "--number", "1")
        self.assertIn("superseded", review["plan"]["proposals"][0]["comment"])
        self.call("execute", "--number", "1", "--publish", "--approve", "bad", ok=False)
        self.assertEqual(self.calls(), [])
        self.call("view", "--number", "1", "--checkpoint", proposal["checkpoint"])
        viewed = self.call("list")["rows"][0]
        self.assertTrue(viewed["active"])
        self.assertFalse(viewed["needs_attention"])
        self.call("dismiss", "--number", "1", "--checkpoint", proposal["checkpoint"])
        self.assertFalse(self.call("list")["rows"][0]["active"])
        self.call("review", "--all", ok=False)
        self.assertEqual(self.calls(), [])

    def test_inspect_reads_both_pr_and_issue_revisions(self):
        observed = self.call("inspect", "--number", "1")
        self.assertEqual(observed["head_sha"], "b" * 40)
        self.assertEqual(observed["updated_at"], self.item["updated_at"])
        self.assertEqual(observed["pr_updated_at"], "2026-09-24T00:00:00Z")
        self.assertEqual(observed["requests"], 2)
        self.assertEqual([method for method, _ in self.calls()], ["GET", "GET"])

    def test_inspect_rejects_changed_target_identity(self):
        self.item["html_url"] = "https://github.com/owner/repo/pull/2"
        (self.mock / "item.json").write_text(json.dumps(self.item))
        self.call("inspect", "--number", "1", ok=False)
        self.assertEqual([method for method, _ in self.calls()], ["GET", "GET"])

    def test_exact_replacement_retains_history_and_reopens_dismissed_proposal(self):
        previous = self.propose()
        self.call("dismiss", "--number", "1", "--checkpoint", previous["checkpoint"])
        self.comment.write_text("Updated explanation after reviewing the new discussion.")
        args = ("propose", "--number", "1", "--title", "Old approach", "--head-sha", "b" * 40,
                "--updated-at", self.item["updated_at"], "--rationale", "New review", "--comment-file", str(self.comment), "--by", "agent:reviewer")
        self.call(*args, ok=False)
        replacement = self.call(*args, "--replace-checkpoint", previous["checkpoint"])
        self.assertTrue(replacement["needs_attention"])
        saved = json.loads((self.root / "data/owner/repo/auto-close/pr-1.json").read_text())
        self.assertEqual(saved["history"][0]["comment"], "This PR has been superseded by #2. Thank you for the work.")
        self.assertNotEqual(saved["request_id"], saved["history"][0]["request_id"])
        self.assertEqual(self.calls(), [])

    def test_exact_review_executes_once_and_retains_separate_outcomes(self):
        self.propose()
        review = self.call("review", "--all")
        result = self.call("execute", "--all", "--publish", "--approve", review["approval"])
        self.assertEqual(result["results"][0]["status"], "executed")
        self.assertEqual(result["results"][0]["outcome"]["comment"]["status"], "succeeded")
        self.assertEqual(result["results"][0]["outcome"]["state_change"]["status"], "succeeded")
        self.assertEqual([method for method, _ in self.calls()], ["GET", "GET", "POST", "PATCH"])
        self.call("execute", "--all", "--publish", "--approve", review["approval"], ok=False)
        self.assertEqual(len(self.calls()), 4)

    def test_changed_head_stops_before_comment_and_keeps_pending(self):
        self.propose()
        review = self.call("review", "--all")
        (self.mock / "head").write_text("c" * 40)
        self.call("execute", "--all", "--publish", "--approve", review["approval"], ok=False)
        self.assertEqual([method for method, _ in self.calls()], ["GET", "GET"])
        self.assertEqual(self.call("list")["rows"][0]["status"], "pending")

    def test_saved_successful_comment_request_is_reconciled_without_reposting(self):
        self.propose()
        review = self.call("review", "--all")
        proposal = json.loads((self.root / "data/owner/repo/auto-close/pr-1.json").read_text())
        base = [str(self.root / "bin/comment-plus"), "--expected-repo", "owner/repo", "--kind", "pr", "--number", "1",
                "--body=" + proposal["comment"], "--close", "--request-id", proposal["request_id"],
                "--expected-head", proposal["head_sha"], "--expected-updated-at", proposal["updated_at"]]
        preview = subprocess.run(base, env=self.env, cwd=self.root, capture_output=True, text=True, check=True)
        approval = json.loads(preview.stdout)["approval"]
        subprocess.run(base + ["--publish", "--approve", approval], env=self.env, cwd=self.root, capture_output=True, text=True, check=True)
        self.assertEqual(len(self.calls()), 4)
        result = self.call("execute", "--all", "--publish", "--approve", review["approval"])
        self.assertEqual(result["results"][0]["status"], "executed")
        self.assertEqual(len(self.calls()), 4)

    def test_failed_close_is_uncertain_and_never_retries_comment(self):
        self.propose()
        review = self.call("review", "--all")
        (self.mock / "fail-patch").touch()
        self.call("execute", "--all", "--publish", "--approve", review["approval"], ok=False)
        row = self.call("list")["rows"][0]
        self.assertEqual(row["status"], "uncertain")
        self.assertEqual(row["outcome"]["comment"]["status"], "succeeded")
        self.assertEqual(row["outcome"]["state_change"]["status"], "unknown")
        self.call("execute", "--all", "--publish", "--approve", review["approval"], ok=False)
        self.assertEqual(len(self.calls()), 4)
