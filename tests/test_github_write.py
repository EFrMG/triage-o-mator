"""Only comment-plus can publish, and its approval binds the exact operation."""

import json

from support import Workspace, item


WRITE_GH = '''#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
root = Path(os.environ["FAKE_GH_DIR"])
args = sys.argv[1:]
method = args[args.index("--method") + 1]
payload = json.load(sys.stdin) if method != "GET" else None
with (root / "write-calls.jsonl").open("a") as out:
    out.write(json.dumps(dict(method=method, body=payload, endpoint=args[-3] if method != "GET" else args[-1])) + "\\n")
if method == "GET":
    print((root / "item.json").read_text())
elif method == "POST":
    print(json.dumps(dict(id=42, html_url="https://github.com/owner/repo/issues/1#issuecomment-42")))
elif method == "PATCH":
    if (root / "fail-patch").exists():
        sys.exit("connection lost")
    item = json.loads((root / "item.json").read_text())
    item["state"] = payload["state"]
    print(json.dumps(item))
else:
    sys.exit("unexpected operation")
'''

CLOSE_GH = '''#!/usr/bin/env python3
import json, os, re, sys
from pathlib import Path
root = Path(os.environ["FAKE_GH_DIR"])
args = sys.argv[1:]
if args[:1] != ["api"] or "--method" not in args:
    sys.exit("unexpected GitHub operation")
method = args[args.index("--method") + 1]
endpoint = args[-3] if method in ("POST", "PATCH") else args[-1]
match = re.fullmatch(r"repos/owner/repo/(issues|pulls)/([12])(/comments)?", endpoint)
if not match:
    sys.exit("unexpected endpoint: " + endpoint)
kind, number, comments = match.groups()
payload = json.load(sys.stdin) if method in ("POST", "PATCH") else None
with (root / "write-calls.jsonl").open("a") as out:
    out.write(json.dumps(dict(method=method, endpoint=endpoint, body=payload)) + "\\n")
if method == "GET" and comments is None:
    print((root / f"{kind}-{number}.json").read_text())
elif method == "POST" and kind == "issues" and comments:
    hook = root / "edit-after-first-comment"
    if number == "1" and hook.exists():
        ledger = root.parent / "data/owner/repo/ledger.jsonl"
        rows = [json.loads(line) for line in ledger.read_text().splitlines()]
        rows[1]["reviewer_notes"] = "Changed while the selected set was running"
        ledger.write_text("\\n".join(json.dumps(row) for row in rows) + "\\n")
        hook.unlink()
    print(json.dumps(dict(id=42, html_url=f"https://github.com/owner/repo/issues/{number}#issuecomment-42")))
elif method == "PATCH" and kind == "pulls" and comments is None:
    if (root / "fail-patch").exists():
        sys.exit("connection lost")
    pull = json.loads((root / f"pulls-{number}.json").read_text())
    pull["state"] = payload["state"]
    print(json.dumps(pull))
else:
    sys.exit("unexpected GitHub operation")
'''


class WriteTests(Workspace):
    def setUp(self):
        super().setUp()
        (self.mock / "gh").write_text(WRITE_GH)
        (self.mock / "item.json").write_text(json.dumps(dict(number=1, html_url="https://github.com/owner/repo/issues/1", state="open")))

    def call(self, *args, body="A proposed comment", ok=True):
        return self.json_cli("comment-plus", "--expected-repo", "owner/repo", "--kind", "issue", "--number", "1", "--body", body, *args, ok=ok)

    def publish(self, plan):
        return ("--publish", "--request-id", plan["plan"]["request_id"], "--approve", plan["approval"])

    def write_calls(self):
        path = self.mock / "write-calls.jsonl"
        return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []

    def test_dry_run_requires_no_network_or_record(self):
        preview = self.call()
        self.assertEqual(preview["plan"]["body"], "A proposed comment")
        self.assertEqual(self.write_calls(), [])
        self.assertFalse((self.root / "data/owner/repo/writes").exists())
        self.call("--publish", ok=False)
        self.assertEqual(self.write_calls(), [])

    def test_approval_binds_comment_and_replay(self):
        preview = self.call()
        args = self.publish(preview)
        self.call(*args, body="Different text", ok=False)
        self.assertEqual(self.write_calls(), [])
        result = self.call(*args)
        self.assertEqual(result["comment"]["status"], "succeeded")
        self.assertEqual(result["state_change"]["status"], "not_requested")
        self.assertEqual(self.call(*args), result)
        self.assertEqual([call["method"] for call in self.write_calls()], ["GET", "POST"])
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())

    def test_comment_and_state_outcomes_are_separate(self):
        preview = self.call("--close")
        (self.mock / "fail-patch").touch()
        result = self.call(*self.publish(preview), "--close", ok=False)
        self.assertEqual(result["comment"]["status"], "succeeded")
        self.assertEqual(result["state_change"]["status"], "unknown")
        (self.mock / "fail-patch").unlink()
        self.call(*self.publish(preview), "--close", ok=False)
        self.assertEqual([call["method"] for call in self.write_calls()], ["GET", "POST", "PATCH"])


class AutoCloseWriteTests(Workspace):
    def setUp(self):
        super().setUp()
        (self.mock / "gh").write_text(CLOSE_GH)
        ledger = self.root / "data/owner/repo/ledger.jsonl"
        ledger.write_text("\n".join(json.dumps(dict(item(number, f"PR {number}", "pr"), reviewer_notes="Initial guidance")) for number in (1, 2)) + "\n")
        for number in (1, 2):
            target = f"https://github.com/owner/repo/pull/{number}"
            (self.mock / f"issues-{number}.json").write_text(json.dumps(dict(number=number, html_url=target, state="open",
                                                                               updated_at="2026-09-29T00:00:00Z", pull_request={})))
            (self.mock / f"pulls-{number}.json").write_text(json.dumps(dict(number=number, html_url=target, head=dict(sha="b" * 40))))

    def auto_close(self, *args, ok=True):
        return self.json_cli("auto-close", "--expected-repo", "owner/repo", *args, ok=ok)

    def propose(self, number, *extra):
        context = self.json_cli("item-context", "--expected-repo", "owner/repo", "read", "--kind", "pr", "--number", str(number))
        comment = self.root / f"comment-{number}.md"
        comment.write_text(f"PR #{number} is superseded.\n")
        return self.auto_close("propose", "--number", str(number), "--title", f"PR {number}", "--head-sha", "b" * 40,
                               "--updated-at", "2026-09-29T00:00:00Z", "--rationale", "Superseded", "--comment-file", str(comment),
                               "--by", "agent:helper", "--context-checkpoint", context["checkpoint"],
                               "--evidence-gap", "No selected snapshot", *extra)

    def write_calls(self):
        path = self.mock / "write-calls.jsonl"
        return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []

    def test_changed_guidance_requires_fresh_review_and_approval(self):
        self.propose(1)
        old = self.auto_close("review", "--number", "1")
        ledger = self.root / "data/owner/repo/ledger.jsonl"
        rows = [json.loads(line) for line in ledger.read_text().splitlines()]
        rows[0]["reviewer_notes"] = "Keep open for compatibility"
        ledger.write_text("\n".join(json.dumps(row) for row in rows) + "\n")

        self.auto_close("review", "--number", "1", ok=False)
        self.auto_close("execute", "--number", "1", "--publish", "--approve", old["approval"], ok=False)
        self.assertEqual(self.write_calls(), [])

        pending = self.auto_close("list")["rows"][0]
        fresh = self.propose(1, "--replace-checkpoint", pending["checkpoint"])
        reviewed = self.auto_close("review", "--number", "1")
        self.assertNotEqual(reviewed["approval"], old["approval"])
        self.auto_close("execute", "--number", "1", "--publish", "--approve", old["approval"], ok=False)
        self.assertEqual(self.write_calls(), [])
        result = self.auto_close("execute", "--number", "1", "--publish", "--approve", reviewed["approval"])
        self.assertEqual(result["results"][0]["status"], "executed")
        self.assertEqual([call["method"] for call in self.write_calls()], ["GET", "GET", "POST", "PATCH"])
        self.assertEqual(self.write_calls()[2]["body"]["body"], fresh["comment"])

    def test_reconsidered_rejection_requires_a_new_exact_approval(self):
        pending = self.propose(1)
        old = self.auto_close("review", "--number", "1")
        rejected = self.auto_close("reject", "--number", "1", "--checkpoint", pending["checkpoint"],
                                   "--by", "maintainer", "--reason", "Keep the old API")
        self.auto_close("execute", "--number", "1", "--publish", "--approve", old["approval"], ok=False)
        self.assertEqual(self.write_calls(), [])

        ledger = self.root / "data/owner/repo/ledger.jsonl"
        rows = [json.loads(line) for line in ledger.read_text().splitlines()]
        rows[0]["reviewer_notes"] = "Old API concern resolved; closure may proceed"
        ledger.write_text("\n".join(json.dumps(row) for row in rows) + "\n")
        fresh = self.propose(1, "--replace-checkpoint", rejected["checkpoint"],
                             "--reconsideration-reason", "Maintainer resolved the old API concern in local guidance")
        reviewed = self.auto_close("review", "--number", "1")
        self.assertNotEqual(reviewed["approval"], old["approval"])
        self.auto_close("execute", "--number", "1", "--publish", "--approve", old["approval"], ok=False)
        self.assertEqual(self.write_calls(), [])

        result = self.auto_close("execute", "--number", "1", "--publish", "--approve", reviewed["approval"])
        self.assertEqual(result["results"][0]["status"], "executed")
        self.assertEqual(self.auto_close("list")["rows"][0]["reconsideration"], fresh["reconsideration"])
        self.assertEqual(self.write_calls()[2]["body"]["body"], fresh["comment"])

    def test_execution_rechecks_remaining_items(self):
        self.propose(1)
        self.propose(2)
        reviewed = self.auto_close("review", "--number", "1", "--number", "2")
        (self.mock / "edit-after-first-comment").touch()
        self.auto_close("execute", "--number", "1", "--number", "2", "--publish", "--approve", reviewed["approval"], ok=False)
        self.assertEqual([call["method"] for call in self.write_calls()], ["GET", "GET", "POST", "PATCH"])
        rows = {row["number"]: row for row in self.auto_close("list")["rows"]}
        self.assertEqual(rows[1]["status"], "executed")
        self.assertEqual(rows[2]["status"], "pending")

        second = self.propose(2, "--replace-checkpoint", rows[2]["checkpoint"])
        self.assertNotEqual(second["checkpoint"], rows[2]["checkpoint"])

    def test_stale_context_does_not_block_uncertain_write_reconciliation(self):
        proposal = self.propose(1)
        reviewed = self.auto_close("review", "--number", "1")
        saved = json.loads((self.root / "data/owner/repo/auto-close/pr-1.json").read_text())
        base = ("--expected-repo", "owner/repo", "--host", "github.com", "--kind", "pr", "--number", "1",
                "--body", proposal["comment"], "--close", "--request-id", saved["request_id"],
                "--expected-head", saved["head_sha"], "--expected-updated-at", saved["updated_at"])
        plan = self.json_cli("comment-plus", *base)
        (self.mock / "fail-patch").touch()
        self.json_cli("comment-plus", *base, "--publish", "--approve", plan["approval"], ok=False)
        (self.mock / "fail-patch").unlink()
        before = self.write_calls()
        ledger = self.root / "data/owner/repo/ledger.jsonl"
        rows = [json.loads(line) for line in ledger.read_text().splitlines()]
        rows[0]["reviewer_notes"] = "Changed after the write attempt"
        ledger.write_text("\n".join(json.dumps(row) for row in rows) + "\n")

        self.auto_close("review", "--number", "1", ok=False)
        outcome = self.auto_close("execute", "--number", "1", "--publish", "--approve", reviewed["approval"], ok=False)
        self.assertEqual(outcome["results"][0]["status"], "uncertain")
        self.assertEqual(self.write_calls(), before)

    def test_group_handoff_rechecks_member_guidance_and_rejection(self):
        group = self.json_cli("group", "create", "--title", "Compare fixes", "--by", "maintainer")
        for number in (1, 2):
            self.run_cli("group", "add", group["id"], "--kind", "pr", "--number", str(number), "--by", "maintainer")

        def handoff_args():
            packet = self.json_cli("group", "export", group["id"], "--format", "json")
            return ("--group-id", group["id"], *[part for member in packet["items"] for part in
                                                 ("--member-context", f"{member['kind']}:{member['number']}:{member['local_context']['checkpoint']}")])

        pending = self.propose(1, *handoff_args())
        ledger = self.root / "data/owner/repo/ledger.jsonl"
        rows = [json.loads(line) for line in ledger.read_text().splitlines()]
        rows[1]["reviewer_notes"] = "Prefer the other fix"
        ledger.write_text("\n".join(json.dumps(row) for row in rows) + "\n")
        context_status = self.auto_close("context", "--number", "1", "--checkpoint", pending["checkpoint"])
        self.assertFalse(context_status["current"])
        self.assertIn("group member context changed", context_status["reason"])
        self.auto_close("review", "--number", "1", ok=False)

        fresh = self.propose(1, *handoff_args(), "--replace-checkpoint", pending["checkpoint"])
        approved = self.auto_close("review", "--number", "1")
        self.run_cli("group", "add", group["id"], "--kind", "pr", "--number", "2", "--notes", "Keep this member open", "--by", "maintainer")
        self.auto_close("execute", "--number", "1", "--publish", "--approve", approved["approval"], ok=False)
        self.assertEqual(self.write_calls(), [])

        rejected = self.auto_close("reject", "--number", "1", "--checkpoint", fresh["checkpoint"],
                                   "--by", "maintainer", "--reason", "Group guidance changed")
        self.assertEqual(rejected["status"], "rejected")
        self.auto_close("execute", "--number", "1", "--publish", "--approve", approved["approval"], ok=False)
        self.assertEqual(self.write_calls(), [])
