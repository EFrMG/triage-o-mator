"""Local review aids preserve the distinction between context and approval."""

import json

from support import Workspace, item, repository, summary


class GroupTests(Workspace):
    def test_ready_group_never_changes_member_decision(self):
        row = dict(item(1, "Related issue"), category="bug", action="label-only", confidence="medium", reason="Proposal", reviewed=False)
        ledger = self.root / "data/owner/repo/ledger.jsonl"
        ledger.write_text(json.dumps(row) + "\n")
        original = ledger.read_bytes()

        group = self.json_cli("group", "create", "--title", "Related reports", "--description", "Review together", "--by", "operator")
        self.run_cli("group", "add", group["id"], "--kind", "issue", "--number", "1", "--by", "operator")
        self.run_cli("group", "update", group["id"], "--status", "ready", "--by", "operator")
        self.assertEqual(ledger.read_bytes(), original)
        self.assertFalse(self.ledger()[("issue", 1)]["reviewed"])
        self.assertEqual(self.calls(), [])


class CandidateTests(Workspace):
    """#1 and #2 rewrite the same original line of a file three open PRs touch, so the shared-file signal is too common to link them; #3 and #4 are a pair a human already confirmed."""

    CHANGES = {
        1: ("Handle a missing adapter", "bin/tool", "if [[ -z $adapter ]]; then return; fi"),
        2: ("Guard power toggle when nothing is paired", "bin/tool", "if [[ -z $adapter ]]; then return; fi"),
        3: ("Stop the theme watcher leaking", "bin/theme", "inotifywait -m \"$dir\" | while read -r event"),
        4: ("Fix duplicated theme reloads", "bin/theme", "inotifywait -m \"$dir\" | while read -r event"),
        5: ("Rename the tool's log file", "bin/tool", "log_file=\"$HOME/.cache/tool.log\""),
    }

    def setUp(self):
        super().setUp()
        self.responses["repos/owner/repo"] = dict(data=repository())
        rows = []
        for number, (title, path, original) in self.CHANGES.items():
            patch = f"@@ -1 +1 @@\n-{original}\n+# changed by #{number}"
            base = f"repos/owner/repo/pulls/{number}"
            self.responses[base] = dict(data=summary(number=number, title=title, changed_files=1, additions=1, deletions=1))
            self.responses[base + "#diff"] = dict(text=f"diff --git a/{path} b/{path}\nindex 1111111..2222222 100644\n--- a/{path}\n+++ b/{path}\n{patch}")
            self.responses[base + "/files?per_page=100&page=1"] = dict(data=[dict(filename=path, status="modified", additions=1, deletions=1, changes=2, patch=patch)])
            self.responses[f"repos/owner/repo/issues/{number}/comments?per_page=100&page=1"] = dict(data=[])
            row = summary(number=number, title=title, id=1000 + number, node_id=f"LIST_{number}")
            row.update(user=dict(login="author"), labels=[], created_at=row["updated_at"], pull_request=dict(url=f"https://api.github.com/{base}"))
            rows.append(row)

        self.responses["repos/owner/repo/issues?state=open&per_page=100&page=1"] = dict(data=rows)
        self.run_cli("fetch", "--cache-inventory")
        snapshot = self.json_cli("cache", "import-inventory")["snapshot_id"]
        self.corpus = self.json_cli("cache", "corpus-create", "--snapshot", snapshot, "--scope", "open-prs", "--profile", "pr-code")["corpus_id"]
        self.assertEqual(self.json_cli("cache", "corpus-run", self.corpus, "--request-budget", "100")["counts"]["complete"], 5)

    def candidates(self):
        before = len(self.calls())
        result = self.json_cli("cache", "candidates", "--corpus", self.corpus, "--max-frequency", "2")
        self.assertEqual(len(self.calls()), before)

        return result

    def test_changed_lines_link_prs_whose_shared_file_is_too_common(self):
        result = self.candidates()
        sets = {tuple(row["members"]): row for row in result["results"] if row["type"] == "candidate-set"}
        self.assertEqual(sorted(sets), [(1, 2), (3, 4)])
        signal = sets[1, 2]["pairs"][0]["signals"][0]
        self.assertEqual((signal["signal"], signal["paths"][0]["sample"]), ("changed_lines", "if [[ -z $adapter ]]; then return; fi"))
        self.assertEqual([row["signal"] for row in sets[1, 2]["pairs"][0]["signals"]], ["changed_lines"])
        self.assertEqual(result["confirmed_duplicates"], [])

    def test_confirmed_duplicate_is_listed_once_and_never_as_a_lead(self):
        decision = dict(category="duplicate-pr", action="close-duplicate", confidence="high", reason="Duplicate of #4 (Fix duplicated theme reloads).")
        rows = [dict(item(3, "Stop the theme watcher leaking", "pr"), **decision, reviewed=True, reviewed_by="maintainer"),
                dict(item(1, "Handle a missing adapter", "pr"), **dict(decision, reason="Duplicate of #2 (Guard power toggle)."), reviewed=False)]
        ledger = self.root / "data/owner/repo/ledger.jsonl"
        ledger.write_text("".join(json.dumps(row) + "\n" for row in rows))
        original = ledger.read_bytes()

        result = self.candidates()
        self.assertEqual([row["members"] for row in result["results"]], [[1, 2]])
        confirmed = result["confirmed_duplicates"]
        self.assertEqual([(row["members"], row["survivor"]) for row in confirmed], [([3, 4], 4)])
        self.assertEqual(confirmed[0]["signals"][0]["signal"], "changed_lines")
        self.assertEqual(ledger.read_bytes(), original)


class WatchTests(Workspace):
    def setUp(self):
        super().setUp()
        self.seed_pr("closed")

    def acquire(self):
        return self.json_cli("cache", "fetch", "--kind", "pr", "--number", "1", "--profile", "closure-watch", "--mode", "refresh", "--request-budget", "100")["snapshot_id"]

    def test_watch_enrollment_and_read_are_offline(self):
        snapshot = self.acquire()
        enrolled = self.json_cli("cache", "watch-enroll", "--number", "1", "--snapshot", snapshot, "--by", "operator", "--closure-event", "99")
        before = len(self.calls())
        shown = self.json_cli("cache", "watch-show", "--number", "1")
        self.assertEqual(shown["watch"], enrolled["watch"])
        self.assertEqual(len(self.calls()), before)
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())

    def test_imported_closure_does_not_enroll_or_approve(self):
        snapshot = self.acquire()
        claim = dict(external=dict(namespace="runner", record_id="run-1"), actor=None, run_id=None, rationale="Retained explanation", survivor=None, provenance="unknown", supports=[])
        path = self.root / "claim.json"
        path.write_text(json.dumps(claim))
        before = len(self.calls())
        imported = self.json_cli("cache", "closure-import", "--number", "1", "--snapshot", snapshot, "--by", "operator", "--claim", str(path), "--closure-event", "99")
        listing = self.json_cli("cache", "action-list")
        self.assertEqual(listing["rows"][0]["history_checkpoint"], imported["history"]["checksum"])
        self.assertEqual(len(self.calls()), before)
        self.assertFalse((self.root / "data/owner/repo/cache/watches/pr-1.json").exists())
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())

    def test_closure_capture_produces_unknown_attribution_for_notifications(self):
        captured = self.json_cli("cache", "closure-capture", "--number", "1", "--by", "operator", "--request-budget", "100")
        entry = captured["history"]["entries"][0]
        self.assertEqual(captured["snapshot_id"], entry["observation"]["snapshot_id"])
        self.assertIsNone(entry["observation"]["operation_id"])
        self.assertEqual([source["component"] for source in entry["observation"]["sources"]], ["summary"])
        self.assertEqual(entry["claim"]["provenance"], "unknown")
        self.assertIsNone(entry["claim"]["rationale"])
        self.assertEqual(self.json_cli("cache", "action-list")["rows"][0]["history_checkpoint"], captured["history"]["checksum"])
        self.assertFalse((self.root / "data/owner/repo/cache/watches/pr-1.json").exists())
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())

        before = len(self.calls())
        self.run_cli("cache", "closure-capture", "--number", "1", "--by", "operator", ok=False)
        self.assertEqual(len(self.calls()), before)

    def test_watch_poll_records_activity_without_approval(self):
        snapshot = self.acquire()
        self.json_cli("cache", "watch-enroll", "--number", "1", "--snapshot", snapshot, "--by", "operator", "--closure-event", "99")
        self.responses["repos/owner/repo/pulls/1"]["data"].update(updated_at="2026-09-23T00:00:00Z", comments=1)
        self.responses["repos/owner/repo/issues/1/comments?per_page=100&page=1"] = dict(data=[dict(id=1, node_id="C_1", html_url="https://github.com/owner/repo/issues/1#issuecomment-1", body="Please reconsider", user=dict(login="author"), created_at="2026-09-23T00:00:00Z", updated_at="2026-09-23T00:00:00Z")])
        self.responses["repos/owner/repo/issues/1/timeline?per_page=100&page=1"]["data"].append(dict(id=1, event="commented", created_at="2026-09-23T00:00:00Z"))
        result = self.json_cli("cache", "watch-poll", "--number", "1", "--restart")
        self.assertEqual(result["observation_count"], 2)
        self.assertEqual(result["poll"]["status"], "complete")
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())
