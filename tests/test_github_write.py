"""Only comment-plus can publish, and its approval binds the exact operation."""

import hashlib
import json
import uuid

from support import FAKE_GH, Workspace, item


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

    def test_issue_activity_change_stops_comment_before_write(self):
        selected = "2026-10-04T01:00:00Z"
        item_path = self.mock / "item.json"
        value = json.loads(item_path.read_text())
        value["updated_at"] = selected
        item_path.write_text(json.dumps(value))
        preview = self.call("--expected-updated-at", selected)

        value["updated_at"] = "2026-10-04T02:00:00Z"
        item_path.write_text(json.dumps(value))
        self.call(*self.publish(preview), "--expected-updated-at", selected, ok=False)
        self.assertEqual([call["method"] for call in self.write_calls()], ["GET"])
        self.assertFalse((self.root / "data/owner/repo/writes" / f"{preview['plan']['request_id']}.json").exists())

    def test_issue_action_stages_exact_comment_and_keeps_rejection_outside_ledger(self):
        timestamp = "2026-10-04T01:00:00Z"
        live = json.loads((self.mock / "item.json").read_text())
        live["updated_at"] = timestamp
        (self.mock / "item.json").write_text(json.dumps(live))
        decision = dict(item(1, "Needs a reproduction"), action="comment", confidence="medium",
                        reason="The report lacks steps", reviewed=False)
        ledger = self.root / "data/owner/repo/ledger.jsonl"
        ledger.write_text(json.dumps(decision) + "\n")
        context = self.json_cli("item-context", "--expected-repo", "owner/repo", "read", "--kind", "issue", "--number", "1")
        comment = self.root / "request-info.md"
        comment.write_text("Could you share steps to reproduce this issue?\n")
        args = ("--expected-repo", "owner/repo")
        saved = self.json_cli("action-proposals", *args, "propose", "--kind", "issue", "--number", "1",
                              "--action", "comment", "--title", "Needs a reproduction", "--observed-state", "open",
                              "--updated-at", timestamp, "--comment-file", str(comment), "--by", "agent:helper",
                              "--context-checkpoint", context["checkpoint"], "--evidence-gap", "Discussion not acquired")
        self.assertTrue(saved["active"])
        self.assertEqual(self.write_calls(), [])
        self.assertEqual(self.json_cli("action-proposals", *args, "list")["rows"][0]["comment"], comment.read_text())
        reviewed = self.json_cli("action-proposals", *args, "review", "--kind", "issue", "--number", "1")
        self.assertEqual(reviewed["plan"]["proposals"][0]["checkpoint"], saved["checkpoint"])

        taxonomy_path = self.root / "config/taxonomy.json"
        taxonomy = json.loads(taxonomy_path.read_text())
        taxonomy["actions"].remove("comment")
        taxonomy_path.write_text(json.dumps(taxonomy))
        legacy_preview = self.json_cli("action-pass", "preview", *args, "--item", "issue:1")
        self.assertEqual(legacy_preview["plan"]["items"][0]["mode"], "stage")
        self.assertEqual(self.json_cli("action-pass", "run", *args, "--item", "issue:1", "--preview-sha256", legacy_preview["preview_sha256"])["results"][0]["status"], "staged")
        taxonomy["actions"].append("comment")
        taxonomy_path.write_text(json.dumps(taxonomy))

        rejected = self.json_cli("action-proposals", *args, "reject", "--kind", "issue", "--number", "1",
                                 "--checkpoint", saved["checkpoint"], "--by", "maintainer", "--reason", "Ask for the version too")
        self.assertEqual(rejected["status"], "rejected")
        self.assertEqual(self.json_cli("item-context", *args, "read", "--kind", "issue", "--number", "1")["feedback_count"], 1)
        self.assertFalse(self.ledger()[("issue", 1)]["reviewed"])
        self.assertEqual(self.write_calls(), [])

        refreshed = self.json_cli("item-context", *args, "read", "--kind", "issue", "--number", "1")
        reconsidered = self.json_cli("action-proposals", *args, "propose", "--kind", "issue", "--number", "1",
                                     "--action", "comment", "--title", "Needs a reproduction", "--observed-state", "open",
                                     "--updated-at", timestamp, "--comment-file", str(comment), "--by", "agent:helper",
                                     "--context-checkpoint", refreshed["checkpoint"], "--evidence-gap", "Discussion not acquired",
                                     "--replace-checkpoint", rejected["checkpoint"], "--reconsideration-reason", "Include the version in the request")
        self.assertEqual(reconsidered["reconsideration"]["rejected_checkpoint"], rejected["checkpoint"])
        path = self.root / "data/owner/repo/action-proposals/issue-1.json"
        corrupt = json.loads(path.read_text())
        corrupt.pop("checksum")
        corrupt.pop("reconsideration")
        canonical = json.dumps(corrupt, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False)
        corrupt["checksum"] = hashlib.sha256(canonical.encode()).hexdigest()
        path.write_text(json.dumps(corrupt))
        self.run_cli("action-proposals", *args, "list", ok=False)

    def test_reopen_action_requires_explanation_and_keeps_gap_staged(self):
        timestamp = "2026-10-04T03:00:00Z"
        live = json.loads((self.mock / "item.json").read_text())
        live.update(state="closed", updated_at=timestamp)
        (self.mock / "item.json").write_text(json.dumps(live))
        decision = dict(item(1, "Closed too early"), state="closed", action="reopen",
                        confidence="high", reason="The reported behavior still reproduces", reviewed=False)
        (self.root / "data/owner/repo/ledger.jsonl").write_text(json.dumps(decision) + "\n")
        args = ("--expected-repo", "owner/repo")
        context = self.json_cli("item-context", *args, "read", "--kind", "issue", "--number", "1")
        comment = self.root / "reopen.md"
        comment.write_text("This still reproduces, so I am reopening it for investigation.\n")
        held = self.json_cli("action-proposals", *args, "propose", "--kind", "issue", "--number", "1",
                             "--action", "reopen", "--title", "Closed too early", "--observed-state", "closed",
                             "--updated-at", timestamp, "--comment-file", str(comment), "--by", "agent:helper",
                             "--context-checkpoint", context["checkpoint"], "--evidence-gap", "Discussion not acquired",
                             "--decision-question", "Should this fixture be reopened?")
        self.json_cli("action-proposals", *args, "answer", "--kind", "issue", "--number", "1",
                      "--checkpoint", held["checkpoint"], "--by", "maintainer", "--answer", "Yes, complete the trial")
        review = self.json_cli("action-proposals", *args, "review", "--kind", "issue", "--number", "1")
        policy_args = (*args, "--action", "reopen", "--mode", "execute")
        policy = self.json_cli("action-policy", "set", *policy_args)
        self.json_cli("action-policy", "set", *policy_args, "--apply", "--preview-sha256", policy["preview_sha256"])
        self.run_cli("action-proposals", *args, "execute", "--kind", "issue", "--number", "1", "--publish",
                     "--approve", review["approval"], "--automation", ok=False)
        self.assertEqual(self.write_calls(), [])

        outcome = self.json_cli("action-proposals", *args, "execute", "--kind", "issue", "--number", "1",
                                "--publish", "--approve", review["approval"])
        self.assertEqual(outcome["status"], "executed")
        self.assertEqual(outcome["outcome"]["state_change"]["state"], "open")
        self.assertEqual([call["method"] for call in self.write_calls()], ["GET", "POST", "PATCH"])
        self.assertEqual(self.write_calls()[1]["body"]["body"], comment.read_text())

        first = self.json_cli("action-proposals", *args, "list")["rows"][0]
        live.update(state="open", updated_at="2026-10-04T04:00:00Z")
        (self.mock / "item.json").write_text(json.dumps(live))
        decision.update(state="open", action="close", reason="Return this synthetic fixture to its starting state")
        (self.root / "data/owner/repo/ledger.jsonl").write_text(json.dumps(decision) + "\n")
        context = self.json_cli("item-context", *args, "read", "--kind", "issue", "--number", "1")
        comment.write_text("Closing this synthetic fixture after the reopen trial.\n")
        next_action = ("--kind", "issue", "--number", "1", "--action", "close", "--title", "Closed too early",
                       "--observed-state", "open", "--updated-at", live["updated_at"], "--comment-file", str(comment),
                       "--by", "agent:helper", "--context-checkpoint", context["checkpoint"],
                       "--evidence-gap", "Discussion not acquired")
        self.run_cli("action-proposals", *args, "propose", *next_action, ok=False)
        second = self.json_cli("action-proposals", *args, "propose", *next_action, "--replace-checkpoint", first["checkpoint"])
        self.assertNotIn("decision_question", second)
        self.assertNotIn("decision_resolution", second)
        record = json.loads((self.root / "data/owner/repo/action-proposals/issue-1.json").read_text())
        self.assertEqual(record["history"][-1]["status"], "executed")
        self.assertEqual(record["history"][-1]["checksum"], first["checkpoint"])
        second_review = self.json_cli("action-proposals", *args, "review", "--kind", "issue", "--number", "1")
        self.assertNotEqual(second_review["approval"], review["approval"])
        self.run_cli("action-proposals", *args, "execute", "--kind", "issue", "--number", "1",
                     "--publish", "--approve", review["approval"], ok=False)
        closed = self.json_cli("action-proposals", *args, "execute", "--kind", "issue", "--number", "1",
                               "--publish", "--approve", second_review["approval"])
        self.assertEqual(closed["outcome"]["state_change"]["state"], "closed")
        self.assertEqual([call["method"] for call in self.write_calls()], ["GET", "POST", "PATCH", "GET", "POST", "PATCH"])

    def test_earlier_ledger_based_question_resolution_remains_readable(self):
        timestamp = "2026-10-04T01:00:00Z"
        decision = dict(item(1, "Needs a reproduction"), action="comment", confidence="medium",
                        reason="Missing steps", reviewed=False)
        (self.root / "data/owner/repo/ledger.jsonl").write_text(json.dumps(decision) + "\n")
        args = ("--expected-repo", "owner/repo")
        context = self.json_cli("item-context", *args, "read", "--kind", "issue", "--number", "1")
        comment = self.root / "legacy-answer.md"
        comment.write_text("Could you share reproduction steps?\n")
        self.json_cli("action-proposals", *args, "propose", "--kind", "issue", "--number", "1",
                      "--action", "comment", "--title", "Needs a reproduction", "--observed-state", "open",
                      "--updated-at", timestamp, "--comment-file", str(comment), "--by", "agent:helper",
                      "--context-checkpoint", context["checkpoint"], "--evidence-gap", "Discussion not acquired",
                      "--decision-question", "Should this request be sent?")
        path = self.root / "data/owner/repo/action-proposals/issue-1.json"
        previous = json.loads(path.read_text())
        previous.pop("checksum")
        previous["decision_review"] = dict(reviewed=False, by="", at="")
        canonical = json.dumps(previous, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False)
        previous["checksum"] = hashlib.sha256(canonical.encode()).hexdigest()
        legacy = dict(previous)
        legacy.pop("checksum")
        legacy.pop("decision_question")
        legacy.pop("decision_review")
        legacy["history"] = [previous]
        legacy["request_id"] = str(uuid.uuid4())
        legacy["decision_resolution"] = dict(by="maintainer", at="2026-10-04T02:00:00Z", reason="Ask for steps",
                                             held_checkpoint=previous["checksum"])
        canonical = json.dumps(legacy, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False)
        legacy["checksum"] = hashlib.sha256(canonical.encode()).hexdigest()
        path.write_text(json.dumps(legacy))

        listed = self.json_cli("action-proposals", *args, "list")["rows"][0]
        self.assertEqual(listed["decision_resolution"]["reason"], "Ask for steps")
        self.assertNotIn("decision_question", listed)
        self.assertEqual(self.json_cli("action-proposals", *args, "review", "--kind", "issue", "--number", "1")["plan"]["proposals"][0]["checkpoint"], listed["checkpoint"])

    def test_action_target_operation_and_comment_edits_invalidate_exact_approval(self):
        timestamp = "2026-10-04T01:00:00Z"
        decision = dict(item(1, "Needs feedback"), action="comment", confidence="medium", reason="A focused response is useful")
        (self.root / "data/owner/repo/ledger.jsonl").write_text(json.dumps(decision) + "\n")
        args = ("--expected-repo", "owner/repo")
        context = self.json_cli("item-context", *args, "read", "--kind", "issue", "--number", "1")
        comment = self.root / "feedback.md"
        comment.write_text("Please clarify the behavior.\n")
        base = ("--kind", "issue", "--number", "1", "--action", "comment", "--title", "Needs feedback",
                "--observed-state", "open", "--updated-at", timestamp, "--comment-file", str(comment),
                "--by", "agent:helper", "--context-checkpoint", context["checkpoint"], "--evidence-gap", "Discussion not acquired")
        self.json_cli("action-proposals", *args, "propose", *base)
        first = self.json_cli("action-proposals", *args, "review", "--kind", "issue", "--number", "1")

        comment.write_text("Please clarify which version shows the behavior.\n")
        saved = self.json_cli("action-proposals", *args, "list")["rows"][0]
        edited = self.json_cli("action-proposals", *args, "edit", "--kind", "issue", "--number", "1",
                               "--checkpoint", saved["checkpoint"], "--comment-file", str(comment), "--by", "maintainer")
        self.run_cli("action-proposals", *args, "execute", "--kind", "issue", "--number", "1", "--publish", "--approve", first["approval"], ok=False)

        self.json_cli("taxonomy-settings", "update-action", "--expected-repo", "owner/repo", "--action", "comment",
                      "--expected", "Use Reason to say what to ask or explain. Draft the exact public comment during the action pass.", "--expected-operation", "comment", "--name", "comment",
                      "--description", "", "--operation", "close")
        changed = self.json_cli("action-proposals", *args, "propose", *base, "--replace-checkpoint", edited["checkpoint"])
        second = self.json_cli("action-proposals", *args, "review", "--kind", "issue", "--number", "1")
        self.assertEqual(second["plan"]["proposals"][0]["operation"], "close")
        self.assertNotEqual(second["approval"], first["approval"])
        self.run_cli("action-proposals", *args, "execute", "--kind", "issue", "--number", "1", "--publish", "--approve", first["approval"], ok=False)

        moved = self.json_cli("action-proposals", *args, "propose", *base, "--host", "enterprise.example",
                              "--replace-checkpoint", changed["checkpoint"])
        third = self.json_cli("action-proposals", *args, "review", "--kind", "issue", "--number", "1")
        self.assertEqual(moved["target"], "https://enterprise.example/owner/repo/issues/1")
        self.assertNotEqual(third["approval"], second["approval"])
        self.run_cli("action-proposals", *args, "execute", "--kind", "issue", "--number", "1", "--publish", "--approve", second["approval"], ok=False)
        self.assertEqual(self.write_calls(), [])

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

    def propose(self, number, *extra, ok=True):
        context = self.json_cli("item-context", "--expected-repo", "owner/repo", "read", "--kind", "pr", "--number", str(number))
        comment = self.root / f"comment-{number}.md"
        comment.write_text(f"PR #{number} is superseded.\n")
        gap = () if "--evidence" in extra else ("--evidence-gap", "No selected snapshot")
        return self.auto_close("propose", "--number", str(number), "--title", f"PR {number}", "--head-sha", "b" * 40,
                               "--updated-at", "2026-09-29T00:00:00Z", "--comment-file", str(comment),
                               "--by", "agent:helper", "--context-checkpoint", context["checkpoint"],
                               *gap, *extra, ok=ok)

    def write_calls(self):
        path = self.mock / "write-calls.jsonl"
        return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []

    def test_repository_policy_routes_bound_pr_closure_through_existing_proposal(self):
        write_gh = (self.mock / "gh").read_text()
        (self.mock / "gh").write_text(FAKE_GH)
        self.seed_pr()
        snapshot = self.json_cli("cache", "fetch", "--kind", "pr", "--number", "1", "--profile", "discussion",
                                 "--mode", "refresh", "--request-budget", "100")["snapshot_id"]
        (self.mock / "gh").write_text(write_gh)

        ledger = self.root / "data/owner/repo/ledger.jsonl"
        rows = [json.loads(line) for line in ledger.read_text().splitlines()]
        rows[0].update(action="close", confidence="high", reason="Superseded by #2")
        ledger.write_text("\n".join(json.dumps(row) for row in rows) + "\n")
        self.propose(1, "--action", "close", "--evidence", f"pr:1:{snapshot}")

        selected = ("--expected-repo", "owner/repo", "--number", "1")
        staged = self.json_cli("action-pass", "preview", *selected)
        self.assertEqual(staged["plan"]["items"][0]["mode"], "stage")
        self.assertEqual(self.json_cli("action-pass", "run", *selected, "--preview-sha256", staged["preview_sha256"])["results"][0]["status"], "staged")
        self.assertEqual(self.write_calls(), [])

        setting = ("--expected-repo", "owner/repo", "--action", "close", "--mode", "execute")
        policy = self.json_cli("action-policy", "set", *setting)
        self.json_cli("action-policy", "set", *setting, "--apply", "--preview-sha256", policy["preview_sha256"])
        self.run_cli("action-pass", "run", *selected, "--preview-sha256", staged["preview_sha256"], ok=False)
        self.assertEqual(self.write_calls(), [])

        rows[1].update(action="close", confidence="high", reason="Also superseded")
        ledger.write_text("\n".join(json.dumps(row) for row in rows) + "\n")
        self.propose(2, "--action", "close")
        gap_review = self.auto_close("review", "--number", "2")
        self.auto_close("execute", "--number", "2", "--publish", "--approve", gap_review["approval"], "--automation", ok=False)
        self.assertEqual(self.write_calls(), [])

        preview = self.json_cli("action-pass", "preview", *selected)
        self.assertEqual(preview["plan"]["items"][0]["mode"], "execute")
        result = self.json_cli("action-pass", "run", *selected, "--preview-sha256", preview["preview_sha256"])
        self.assertEqual(result["results"][0]["status"], "executed")
        self.assertEqual([call["method"] for call in self.write_calls()], ["GET", "GET", "POST", "PATCH"])
        self.assertEqual({row["number"]: row["status"] for row in self.auto_close("list")["rows"]}, {1: "executed", 2: "pending"})
        self.assertTrue(next(row for row in self.auto_close("list")["rows"] if row["number"] == 1)["needs_attention"])
        record = json.loads((self.root / "data/owner/repo/auto-close/pr-1.json").read_text())
        self.assertEqual(record["outcome"]["authorization"]["source"], "repository-policy")

    def test_questioned_pr_closure_stays_staged_until_human_answers_exact_action(self):
        write_gh = (self.mock / "gh").read_text()
        (self.mock / "gh").write_text(FAKE_GH)
        self.seed_pr()
        snapshot = self.json_cli("cache", "fetch", "--kind", "pr", "--number", "1", "--profile", "discussion",
                                 "--mode", "refresh", "--request-budget", "100")["snapshot_id"]
        (self.mock / "gh").write_text(write_gh)

        ledger = self.root / "data/owner/repo/ledger.jsonl"
        rows = [json.loads(line) for line in ledger.read_text().splitlines()]
        rows[0].update(action="close", confidence="high", reason="Possibly superseded by #2")
        ledger.write_text("\n".join(json.dumps(row) for row in rows) + "\n")
        held = self.propose(1, "--action", "close", "--evidence", f"pr:1:{snapshot}",
                            "--decision-question", "Does #2 fully replace this PR?")
        setting = ("--expected-repo", "owner/repo", "--action", "close", "--mode", "execute")
        policy = self.json_cli("action-policy", "set", *setting)
        self.json_cli("action-policy", "set", *setting, "--apply", "--preview-sha256", policy["preview_sha256"])

        selected = ("--expected-repo", "owner/repo", "--number", "1")
        preview = self.json_cli("action-pass", "preview", *selected)
        self.assertEqual(preview["plan"]["items"][0]["mode"], "stage")
        self.assertEqual(self.json_cli("action-pass", "run", *selected, "--preview-sha256", preview["preview_sha256"])["results"][0]["status"], "staged")
        self.auto_close("review", "--number", "1", ok=False)
        self.assertEqual(self.write_calls(), [])

        answered = self.auto_close("answer", "--number", "1", "--checkpoint", held["checkpoint"],
                                    "--by", "maintainer", "--answer", "Yes, #2 covers the same behavior")
        self.assertEqual(answered["decision_question"], "Does #2 fully replace this PR?")
        self.assertEqual(answered["decision_resolution"]["by"], "maintainer")
        self.assertFalse(self.ledger()[("pr", 1)].get("reviewed", False))
        self.assertEqual(self.json_cli("action-pass", "preview", *selected)["plan"]["items"][0]["mode"], "stage")
        reviewed = self.auto_close("review", "--number", "1")
        exact = reviewed["plan"]["proposals"][0]
        self.assertEqual(reviewed["plan"]["operation"], "comment-and-close-pr")
        self.assertEqual(exact["target"], "https://github.com/owner/repo/pull/1")
        self.assertEqual(exact["comment"], answered["comment"])
        self.assertEqual(exact["decision_resolution"]["reason"], "Yes, #2 covers the same behavior")
        self.auto_close("execute", "--number", "1", "--publish", "--approve", reviewed["approval"], "--automation", ok=False)
        self.assertEqual(self.write_calls(), [])

        outcome = self.auto_close("execute", "--number", "1", "--publish", "--approve", reviewed["approval"])
        self.assertEqual(outcome["results"][0]["status"], "executed")
        self.assertEqual([call["method"] for call in self.write_calls()], ["GET", "GET", "POST", "PATCH"])
        self.assertFalse(self.ledger()[("pr", 1)].get("reviewed", False))

    def test_earlier_pr_closure_resolution_history_remains_readable(self):
        self.propose(1, "--decision-question", "Does the replacement cover this PR?")
        path = self.root / "data/owner/repo/auto-close/pr-1.json"
        previous = json.loads(path.read_text())
        previous.pop("checksum")
        previous["decision_review"] = dict(reviewed=False, by="", at="")
        canonical = json.dumps(previous, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False)
        previous["checksum"] = hashlib.sha256(canonical.encode()).hexdigest()
        legacy = dict(previous)
        legacy.pop("checksum")
        legacy.pop("decision_question")
        legacy.pop("decision_review")
        legacy["history"] = [previous]
        legacy["request_id"] = str(uuid.uuid4())
        legacy["decision_resolution"] = dict(by="maintainer", at="2026-10-04T02:00:00Z", reason="Replacement covers it",
                                             held_checkpoint=previous["checksum"])
        canonical = json.dumps(legacy, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False)
        legacy["checksum"] = hashlib.sha256(canonical.encode()).hexdigest()
        path.write_text(json.dumps(legacy))

        listed = self.auto_close("list")["rows"][0]
        self.assertEqual(listed["decision_resolution"]["reason"], "Replacement covers it")
        self.assertNotIn("decision_question", listed)
        self.assertEqual(self.auto_close("review", "--number", "1")["plan"]["proposals"][0]["checkpoint"], listed["checkpoint"])

    def test_policy_routes_pr_comment_without_creating_a_closure_proposal(self):
        write_gh = (self.mock / "gh").read_text()
        (self.mock / "gh").write_text(FAKE_GH)
        self.seed_pr()
        snapshot = self.json_cli("cache", "fetch", "--kind", "pr", "--number", "1", "--profile", "discussion",
                                 "--mode", "refresh", "--request-budget", "100")["snapshot_id"]
        (self.mock / "gh").write_text(write_gh)

        ledger = self.root / "data/owner/repo/ledger.jsonl"
        rows = [json.loads(line) for line in ledger.read_text().splitlines()]
        rows[0].update(action="comment", confidence="high", reason="The change needs a focused follow-up")
        ledger.write_text("\n".join(json.dumps(row) for row in rows) + "\n")
        context = self.json_cli("item-context", "--expected-repo", "owner/repo", "read", "--kind", "pr", "--number", "1")
        comment = self.root / "feedback.md"
        comment.write_text("Please add coverage for the changed behavior.\n")
        proposal_args = ("--kind", "pr", "--number", "1", "--action", "comment", "--title", "PR 1",
                         "--observed-state", "open", "--updated-at", "2026-09-29T00:00:00Z", "--head-sha", "b" * 40,
                         "--comment-file", str(comment), "--by", "agent:helper", "--evidence", f"pr:1:{snapshot}")
        held = self.json_cli("action-proposals", "--expected-repo", "owner/repo", "propose", *proposal_args,
                             "--context-checkpoint", context["checkpoint"], "--decision-question", "Should this feedback be sent now?")
        self.auto_close("propose", "--number", "1", "--title", "PR 1", "--head-sha", "b" * 40,
                        "--updated-at", "2026-09-29T00:00:00Z", "--comment-file", str(comment), "--by", "agent:helper",
                        "--context-checkpoint", context["checkpoint"], "--evidence-gap", "No selected close evidence", ok=False)
        selection = ("--expected-repo", "owner/repo", "--item", "pr:1")
        self.assertEqual(self.json_cli("action-pass", "preview", *selection)["plan"]["items"][0]["mode"], "stage")

        setting = ("--expected-repo", "owner/repo", "--action", "comment", "--mode", "execute")
        policy = self.json_cli("action-policy", "set", *setting)
        self.json_cli("action-policy", "set", *setting, "--apply", "--preview-sha256", policy["preview_sha256"])
        held_preview = self.json_cli("action-pass", "preview", *selection)
        self.assertEqual(held_preview["plan"]["items"][0]["mode"], "stage")
        self.assertEqual(held_preview["plan"]["items"][0]["decision_question"], "Should this feedback be sent now?")
        self.assertEqual(self.json_cli("action-pass", "run", *selection, "--preview-sha256", held_preview["preview_sha256"])["results"][0]["status"], "staged")
        self.run_cli("action-proposals", "--expected-repo", "owner/repo", "review", "--kind", "pr", "--number", "1", ok=False)
        self.assertEqual(self.write_calls(), [])

        answered = self.json_cli("action-proposals", "--expected-repo", "owner/repo", "answer", "--kind", "pr", "--number", "1",
                                 "--checkpoint", held["checkpoint"], "--by", "maintainer", "--answer", "Send focused feedback now")
        self.assertEqual(answered["decision_resolution"]["by"], "maintainer")
        self.assertEqual(answered["decision_question"], "Should this feedback be sent now?")
        self.assertFalse(self.ledger()[("pr", 1)].get("reviewed", False))
        preview = self.json_cli("action-pass", "preview", *selection)
        self.assertEqual(preview["plan"]["items"][0]["mode"], "stage")
        self.assertEqual(self.json_cli("action-pass", "run", *selection, "--preview-sha256", preview["preview_sha256"])["results"][0]["status"], "staged")
        reviewed = self.json_cli("action-proposals", "--expected-repo", "owner/repo", "review", "--kind", "pr", "--number", "1")
        exact = reviewed["plan"]["proposals"][0]
        self.assertEqual(exact["target"], "https://github.com/owner/repo/pull/1")
        self.assertEqual(exact["operation"], "comment")
        self.assertEqual(exact["comment"], comment.read_text())
        self.assertEqual(exact["decision_resolution"]["reason"], "Send focused feedback now")
        self.run_cli("action-proposals", "--expected-repo", "owner/repo", "execute", "--kind", "pr", "--number", "1",
                     "--publish", "--approve", reviewed["approval"], "--automation", ok=False)
        self.assertEqual(self.write_calls(), [])

        comment.write_text("Please add a focused test for the changed behavior.\n")
        edited = self.json_cli("action-proposals", "--expected-repo", "owner/repo", "edit", "--kind", "pr", "--number", "1",
                               "--checkpoint", answered["checkpoint"], "--comment-file", str(comment), "--by", "maintainer")
        self.assertEqual(edited["decision_resolution"]["reason"], "Send focused feedback now")
        self.run_cli("action-proposals", "--expected-repo", "owner/repo", "execute", "--kind", "pr", "--number", "1",
                     "--publish", "--approve", reviewed["approval"], ok=False)
        self.assertEqual(self.write_calls(), [])
        fresh = self.json_cli("action-proposals", "--expected-repo", "owner/repo", "review", "--kind", "pr", "--number", "1")
        self.assertNotEqual(fresh["approval"], reviewed["approval"])
        result = self.json_cli("action-proposals", "--expected-repo", "owner/repo", "execute", "--kind", "pr", "--number", "1",
                               "--publish", "--approve", fresh["approval"])
        self.assertEqual(result["status"], "executed")
        self.assertEqual([call["method"] for call in self.write_calls()], ["GET", "GET", "POST"])
        self.assertFalse((self.root / "data/owner/repo/auto-close/pr-1.json").exists())
        self.assertTrue(self.json_cli("action-proposals", "--expected-repo", "owner/repo", "list")["rows"][0]["needs_attention"])
        self.assertFalse(self.ledger()[("pr", 1)].get("reviewed", False))
        self.assertEqual(self.json_cli("item-context", "--expected-repo", "owner/repo", "read", "--kind", "pr", "--number", "1")["feedback_count"], 1)

    def test_changed_guidance_requires_fresh_review_and_approval(self):
        self.assertNotIn("rationale", self.propose(1))
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

    def test_pending_edit_retains_history_and_invalidates_selected_plan(self):
        first = self.propose(1, "--rationale", "Legacy assessment")
        self.propose(2)
        old = self.auto_close("review", "--number", "1", "--number", "2")
        comment = self.root / "edited-comment.md"
        comment.write_text("The maintainer revised this explanation.\n")

        edited = self.auto_close("edit", "--number", "1", "--checkpoint", first["checkpoint"],
                                 "--comment-file", str(comment), "--by", "maintainer")
        record = json.loads((self.root / "data/owner/repo/auto-close/pr-1.json").read_text())
        self.assertEqual(edited["comment"], comment.read_text())
        self.assertNotIn("rationale", edited)
        self.assertEqual(record["history"][-1]["rationale"], "Legacy assessment")
        self.assertEqual(record["proposed_by"], "maintainer")
        self.assertEqual(record["history"][-1]["checksum"], first["checkpoint"])
        self.assertEqual(record["inputs"], record["history"][-1]["inputs"])
        self.assertNotEqual(record["request_id"], record["history"][-1]["request_id"])
        self.auto_close("edit", "--number", "1", "--checkpoint", first["checkpoint"],
                        "--comment-file", str(comment), "--by", "maintainer", ok=False)
        self.auto_close("execute", "--number", "1", "--number", "2", "--publish", "--approve", old["approval"], ok=False)
        self.assertEqual(self.write_calls(), [])

        receipt = self.root / "data/owner/repo/writes" / (record["request_id"] + ".json")
        receipt.parent.mkdir(exist_ok=True)
        receipt.write_text("{}\n")
        self.auto_close("edit", "--number", "1", "--checkpoint", edited["checkpoint"],
                        "--comment-file", str(comment), "--by", "maintainer", ok=False)
        self.assertEqual(json.loads((self.root / "data/owner/repo/auto-close/pr-1.json").read_text()), record)
        receipt.unlink()

        fresh = self.auto_close("review", "--number", "1", "--number", "2")
        self.assertNotEqual(fresh["approval"], old["approval"])
        self.auto_close("execute", "--number", "1", "--number", "2", "--publish", "--approve", fresh["approval"])
        self.assertEqual(self.write_calls()[2]["body"]["body"], comment.read_text())
        self.auto_close("edit", "--number", "1", "--checkpoint", edited["checkpoint"],
                        "--comment-file", str(comment), "--by", "maintainer", ok=False)

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

    def test_successful_group_batch_keeps_remaining_approved_context(self):
        group = self.json_cli("group", "create", "--title", "Compare fixes", "--by", "maintainer")
        for number in (1, 2):
            self.run_cli("group", "add", group["id"], "--kind", "pr", "--number", str(number), "--by", "maintainer")

        packet = self.json_cli("group", "export", group["id"], "--format", "json")
        handoff = ("--group-id", group["id"], *[part for member in packet["items"] for part in
                                               ("--member-context", f"{member['kind']}:{member['number']}:{member['local_context']['checkpoint']}")])
        self.propose(1, *handoff)
        self.propose(2, *handoff)
        approved = self.auto_close("review", "--number", "1", "--number", "2")
        result = self.auto_close("execute", "--number", "1", "--number", "2", "--publish", "--approve", approved["approval"])

        self.assertEqual([row["status"] for row in result["results"]], ["executed", "executed"])
        self.assertEqual([call["method"] for call in self.write_calls()], ["GET", "GET", "POST", "PATCH"] * 2)
        self.assertEqual({row["number"]: row["status"] for row in self.auto_close("list")["rows"]}, {1: "executed", 2: "executed"})
        context = self.json_cli("item-context", "--expected-repo", "owner/repo", "read", "--kind", "pr", "--number", "1")
        self.assertEqual(next(row for row in context["rows"] if row["kind"] == "feedback")["fields"]["kind"]["preview"], "write_outcome")

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
