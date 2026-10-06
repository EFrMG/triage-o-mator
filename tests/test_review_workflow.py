"""Local review aids preserve the distinction between context and approval."""

import hashlib
import json

from support import Workspace, item, repository, summary


class CandidateTests(Workspace):
    def test_changed_lines_require_same_position_and_meaningful_coverage(self):
        self.responses["repos/owner/repo"] = dict(data=repository())
        rows = []
        changes = {
            1: (10, "if [[ -z $adapter ]]; then return; fi", 0),
            2: (10, "if [[ -z $adapter ]]; then return; fi", 0),
            3: (500, "if [[ -z $adapter ]]; then return; fi", 0),
            4: (20, "return stale_widget_state_for_current_view", 20),
            5: (20, "return stale_widget_state_for_current_view", 20),
            6: (30, "return consistent_notification_state", 20),
            7: (30, "return consistent_notification_state", 20),
            8: (30, "return consistent_notification_state", 20),
        }
        titles = {1: "Adapter guard", 2: "Power toggle", 3: "Theme watcher", 4: "Archive checksum", 5: "Socket reconnect",
                  6: "Notification persistence", 7: "Dismissed activity", 8: "Session restoration"}
        for number, (position, original, extra) in changes.items():
            path = "src/core.py"
            patch = f"@@ -{position},{extra + 1} +{position},{extra + 1} @@\n-{original}\n+replacement for {number}\n"
            for index in range(extra):
                previous = "shared original state transition" if number in (6, 7, 8) and index == 0 else f"unrelated original line {number}-{index}"
                patch += f"-{previous}\n+unrelated replacement line {number}-{index}\n"

            patch = patch.rstrip("\n")
            base = f"repos/owner/repo/pulls/{number}"
            base_revision = dict(sha=("c" if number == 2 else "a") * 40, ref="main", repo=repository())
            self.responses[base] = dict(data=summary(number=number, title=titles[number], base=base_revision, changed_files=1, additions=extra + 1, deletions=extra + 1))
            self.responses[base + "#diff"] = dict(text=f"diff --git a/{path} b/{path}\nindex 1111111..2222222 100644\n--- a/{path}\n+++ b/{path}\n{patch}")
            self.responses[base + "/files?per_page=100&page=1"] = dict(data=[dict(filename=path, status="modified", additions=extra + 1, deletions=extra + 1, changes=2 * (extra + 1), patch=patch)])
            self.responses[f"repos/owner/repo/issues/{number}/comments?per_page=100&page=1"] = dict(data=[])
            row = summary(number=number, title=titles[number], id=1000 + number, node_id=f"LIST_{number}")
            row.update(user=dict(login="author"), labels=[], created_at=row["updated_at"], pull_request=dict(url=f"https://api.github.com/{base}"))
            rows.append(row)

        self.responses["repos/owner/repo/issues?state=open&per_page=100&page=1"] = dict(data=rows)
        self.run_cli("fetch", "--cache-inventory")
        snapshot = self.json_cli("cache", "import-inventory")["snapshot_id"]
        corpus = self.json_cli("cache", "corpus-create", "--snapshot", snapshot, "--scope", "open-prs", "--profile", "pr-code")["corpus_id"]
        run = self.json_cli("cache", "corpus-run", corpus, "--request-budget", "100")
        self.assertEqual(run["counts"]["complete"], 8, run["current_problems"])

        before = len(self.calls())
        result = self.json_cli("cache", "candidates", "--corpus", corpus, "--max-frequency", "2")
        self.assertEqual(len(self.calls()), before)
        sets = [row for row in result["results"] if row["type"] == "candidate-set"]
        self.assertEqual([row["members"] for row in sets], [[1, 2], [6, 7, 8]])
        self.assertEqual(sets[0]["pairs"][0]["signals"][0]["signal"], "changed_lines")
        self.assertEqual(sets[0]["pairs"][0]["signals"][0]["coverage"], 1.0)
        self.assertEqual(sets[0]["pairs"][0]["signals"][0]["line_coverage"], 0.5)
        self.assertIn("base-revisions-require-reconciliation", sets[0]["holds"])
        self.assertEqual(sets[0]["pairs"][0]["signals"][0]["shared_lines"], 1)
        self.assertEqual(sets[1]["pairs"][0]["signals"][0]["shared_lines"], 2)
        self.assertLess(sets[1]["pairs"][0]["signals"][0]["line_coverage"], 0.1)
        narrower = self.json_cli("cache", "candidates", "--corpus", corpus, "--max-frequency", "2", "--max-line-frequency", "2")
        self.assertEqual([row["members"] for row in narrower["results"] if row["type"] == "candidate-set"], [[1, 2]])
        self.assertEqual(len(self.calls()), before)
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())

        packet = self.root / "candidates.json"
        packet.write_text(json.dumps(result))
        group = self.json_cli("group", "create-candidate", "--file", str(packet), "--candidate", sets[0]["id"], "--by", "agent:review")
        self.assertEqual(group["candidate_origin"]["policy"], "pr-candidate-sets-v2")
        self.assertEqual([member["number"] for member in group["members"]], [1, 2])
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())

        ledger = self.root / "data/owner/repo/ledger.jsonl"
        ledger.write_text("".join(json.dumps(item(number, titles[number], "pr")) + "\n" for number in (1, 2)))
        self.run_cli("not-duplicate", "--key", "pr:1", "--key", "pr:2", "--by", "reviewer", "--note", "different causes")
        excluded = self.json_cli("cache", "candidates", "--corpus", corpus, "--max-frequency", "2")
        self.assertEqual(excluded["candidate_sets"], 1)
        self.assertEqual(excluded["excluded_pairs"], 1)
        excluded_pair = next(row for row in excluded["results"] if row["type"] == "excluded-pair")
        self.assertEqual(excluded_pair["reasons"], ["recorded-negative-verdict"])
        self.assertEqual(len(self.calls()), before)


class GroupTests(Workspace):
    def test_ready_group_never_changes_member_decision(self):
        row = dict(item(1, "Related issue"), category="bug", action="no-action-needed", confidence="medium", reason="Proposal", reviewed=False)
        ledger = self.root / "data/owner/repo/ledger.jsonl"
        ledger.write_text(json.dumps(row) + "\n")
        original = ledger.read_bytes()

        group = self.json_cli("group", "create", "--title", "Related reports", "--description", "Review together", "--by", "operator")
        self.run_cli("group", "add", group["id"], "--kind", "issue", "--number", "1", "--by", "operator")
        self.run_cli("group", "update", group["id"], "--status", "ready", "--by", "operator")
        self.assertEqual(ledger.read_bytes(), original)
        self.assertFalse(self.ledger()[("issue", 1)]["reviewed"])
        self.assertEqual(self.calls(), [])

    def test_group_export_carries_shared_context_and_missing_members(self):
        first = dict(item(1, "First fix", "pr"), category="enhancement", action="keep-open", confidence="high",
                     reason="Wait for the dependency", triaged_by="maintainer", agent_notes="Agent comparison",
                     reviewed=True, reviewed_by="reviewer", reviewer_notes="Check compatibility")
        second = item(2, "Related issue")
        ledger = self.root / "data/owner/repo/ledger.jsonl"
        ledger.write_text(json.dumps(first) + "\n" + json.dumps(second) + "\n")

        group = self.json_cli("group", "create", "--title", "Decide on the fix", "--description", "Compare both members", "--by", "agent:helper")
        self.run_cli("group", "add", group["id"], "--kind", "pr", "--number", "1", "--notes", "[fix candidate]", "--by", "maintainer")
        self.run_cli("group", "add", group["id"], "--kind", "issue", "--number", "2", "--notes", "[report]", "--by", "maintainer")
        other = self.json_cli("group", "create", "--title", "Dependency decision", "--description", "Human guidance", "--by", "maintainer")
        self.run_cli("group", "add", other["id"], "--kind", "pr", "--number", "1", "--notes", "Wait until version 2", "--by", "maintainer")

        ledger.write_text(json.dumps(first) + "\n")
        original = ledger.read_bytes()
        packet = self.json_cli("group", "export", group["id"], "--format", "json")
        by_key = {(entry["kind"], entry["number"]): entry for entry in packet["items"]}
        context = by_key[("pr", 1)]["local_context"]
        direct = self.json_cli("item-context", "--expected-repo", "owner/repo", "read", "--kind", "pr", "--number", "1")

        self.assertEqual(packet["group"]["revision"], 3)
        self.assertEqual(context["context_revision"], direct["context_revision"])
        self.assertEqual(context["ledger"]["reviewer_notes"], "Check compatibility")
        self.assertEqual(context["relevant_group_ids"], sorted((group["id"], other["id"])))
        self.assertEqual([entry["id"] for entry in packet["related_groups"]], [other["id"]])
        self.assertEqual(packet["related_groups"][0]["members"][0]["notes"], "Wait until version 2")
        self.assertTrue(by_key[("issue", 2)]["missing_from_ledger"])
        self.assertFalse(by_key[("issue", 2)]["local_context"]["ledger_present"])

        markdown = self.run_cli("group", "export", group["id"]).stdout
        self.assertIn("Local context revision:", markdown)
        self.assertIn("Dependency decision", markdown)
        self.assertIn("Check compatibility", markdown)
        self.assertNotIn("- labels: []", markdown)
        self.assertNotIn("- last_synced_at: ", markdown)
        self.assertEqual(ledger.read_bytes(), original)
        self.assertEqual(self.calls(), [])

        self.seed_pr()
        snapshot = self.json_cli("cache", "fetch", "--kind", "pr", "--number", "1", "--profile", "discussion",
                                 "--mode", "refresh", "--request-budget", "100")["snapshot_id"]
        before = len(self.calls())
        enriched = self.json_cli("group", "export", group["id"], "--enrich", "--cache-mode", "offline", "--format", "json")
        evidence = {(entry["kind"], entry["number"]): entry["evidence"] for entry in enriched["items"]}
        self.assertEqual(evidence[("pr", 1)]["snapshot_id"], snapshot)
        self.assertIsNone(evidence[("issue", 2)]["snapshot_id"])
        self.assertIn("missing", evidence[("issue", 2)]["problems"]["summary"])
        self.assertEqual(len(self.calls()), before)
        self.assertEqual(ledger.read_bytes(), original)


class ItemContextTests(Workspace):
    def context(self, *args, ok=True):
        return self.json_cli("item-context", "--expected-repo", "owner/repo", *args, ok=ok)

    def test_offline_context_pages_and_revision_checked_text(self):
        absent = self.context("read", "--kind", "pr", "--number", "2")
        self.assertFalse(absent["ledger_present"])
        self.assertEqual(absent["group_count"], 0)
        self.assertEqual(absent["rows"][0]["fields"], {})

        row = dict(item(1, "Keep this change", "pr"), category="enhancement", action="keep-open", confidence="high",
                   reason="Maintainer wants the work", triaged_by="maintainer", triaged_at="2026-09-29T00:00:00Z",
                   agent_notes="Agent comparison", reviewed=True, reviewed_by="reviewer", reviewed_at="2026-09-29T01:00:00Z",
                   reviewer_notes="Wait for the dependency", last_synced_at="2026-09-29T02:00:00Z")
        ledger = self.root / "data/owner/repo/ledger.jsonl"
        ledger.write_text(json.dumps(row) + "\n")
        notes = "é" * 300
        group = self.json_cli("group", "create", "--title", "Shared decision", "--description", "Compare the fixes", "--by", "maintainer")
        self.run_cli("group", "add", group["id"], "--kind", "pr", "--number", "1", "--notes", notes, "--by", "maintainer")

        first = self.context("read", "--kind", "pr", "--number", "1", "--limit", "2")
        self.assertEqual(first["requests"], 0)
        self.assertTrue(first["ledger_present"])
        self.assertEqual(first["rows"][0]["fields"]["reviewed"], True)
        self.assertEqual(first["rows"][0]["fields"]["reviewer_notes"]["preview"], "Wait for the dependency")
        self.assertEqual(first["rows"][1]["revision"], 2)
        self.assertEqual(first["pagination"]["omitted_after"], 1)
        self.context("read", "--kind", "pr", "--number", "1", "--offset", "2", ok=False)

        second = self.context("read", "--kind", "pr", "--number", "1", "--offset", "2", "--limit", "2", "--checkpoint", first["checkpoint"])
        member = second["rows"][0]
        self.assertEqual(member["fields"]["updated_by"]["preview"], "maintainer")
        self.assertGreater(member["fields"]["notes"]["omitted_bytes"], 0)

        source = self.context("source", "--kind", "pr", "--number", "1", "--row", member["id"], "--field", "notes",
                              "--checkpoint", first["checkpoint"], "--max-bytes", "5")
        self.assertEqual(source["text"], "éé")
        self.assertEqual(source["continuation"]["byte_offset"], 4)
        full_note = self.context("source", "--kind", "pr", "--number", "1", "--row", member["id"], "--field", "notes",
                                 "--checkpoint", first["checkpoint"], "--max-bytes", "65536")
        self.assertEqual(full_note["text"], notes)
        self.assertIsNone(full_note["continuation"])

        row["last_synced_at"] = "2026-09-29T03:00:00Z"
        ledger.write_text(json.dumps(row) + "\n")
        self.assertEqual(self.context("read", "--kind", "pr", "--number", "1")["context_revision"], first["context_revision"])

        self.run_cli("group", "add", group["id"], "--kind", "pr", "--number", "1", "--notes", "New guidance", "--by", "maintainer")
        changed = self.context("read", "--kind", "pr", "--number", "1")
        self.assertNotEqual(changed["context_revision"], first["context_revision"])
        self.context("source", "--kind", "pr", "--number", "1", "--row", member["id"], "--field", "notes",
                     "--checkpoint", first["checkpoint"], ok=False)
        self.assertEqual(self.calls(), [])


class ProposalFeedbackTests(Workspace):
    def auto_close(self, *args, ok=True):
        command, *rest = args
        if command in ("review", "execute", "context", "edit", "answer", "reject", "view", "dismiss"):
            rest = ["--kind", "pr", *rest]
        return self.json_cli("action-proposals", "--expected-repo", "owner/repo", command, *rest, ok=ok)

    def propose(self, number, *extra, ok=True):
        comment = self.root / "comment.md"
        comment.write_text("Thanks for the work; this PR is superseded.\n")
        context = self.json_cli("item-context", "--expected-repo", "owner/repo", "read", "--kind", "pr", "--number", str(number))

        return self.auto_close("propose", "--kind", "pr", "--number", str(number), "--operation", "close", "--observed-state", "open", "--title", "An older fix",
                               "--head-sha", "b" * 40, "--updated-at", "2026-09-29T00:00:00Z",
                               "--comment-file", str(comment), "--by", "agent:helper",
                               "--context-checkpoint", context["checkpoint"], *extra, ok=ok)

    def test_proposal_binds_group_context_and_verified_selected_evidence(self):
        self.seed_pr()
        snapshot = self.json_cli("cache", "fetch", "--kind", "pr", "--number", "1", "--profile", "discussion",
                                 "--mode", "refresh", "--request-budget", "100")["snapshot_id"]
        ledger = self.root / "data/owner/repo/ledger.jsonl"
        ledger.write_text(json.dumps(item(1, "Candidate PR", "pr")) + "\n" + json.dumps(item(2, "Original report")) + "\n")
        group = self.json_cli("group", "create", "--title", "Compare options", "--by", "maintainer")
        self.run_cli("group", "add", group["id"], "--kind", "pr", "--number", "1", "--notes", "Keep if compatible", "--by", "maintainer")
        self.run_cli("group", "add", group["id"], "--kind", "issue", "--number", "2", "--notes", "Original report", "--by", "maintainer")
        packet = self.json_cli("group", "export", group["id"], "--format", "json")
        members = [f"{item['kind']}:{item['number']}:{item['local_context']['checkpoint']}" for item in packet["items"]]
        before = len(self.calls())

        selected = self.propose(1, "--group-id", group["id"], *[part for member in members for part in ("--member-context", member)],
                                "--evidence", f"pr:1:{snapshot}", "--evidence-gap", "Issue #2 has no selected evidence")
        inputs = selected["inputs"]
        self.assertEqual(inputs["group"]["id"], group["id"])
        self.assertEqual([(row["kind"], row["number"]) for row in inputs["group"]["members"]], [("pr", 1), ("issue", 2)])
        self.assertEqual(inputs["evidence"][0]["snapshot_id"], snapshot)
        self.assertIn("summary", inputs["evidence"][0]["components"])
        self.assertEqual(inputs["evidence_gaps"], ["Issue #2 has no selected evidence"])
        approved = self.auto_close("review", "--number", "1")
        self.assertEqual(approved["plan"]["proposals"][0]["inputs"], inputs)
        self.assertEqual(len(self.calls()), before)

        object_path = next(path for path in (self.root / "data/owner/repo/cache/objects").iterdir() if path.is_file())
        original_object = object_path.read_bytes()
        object_path.write_text("{}\n")
        unavailable = self.auto_close("context", "--number", "1", "--checkpoint", selected["checkpoint"])
        self.assertFalse(unavailable["current"])
        self.assertIn("selected evidence", unavailable["reason"])
        self.auto_close("review", "--number", "1", ok=False)
        self.auto_close("execute", "--number", "1", "--publish", "--approve", approved["approval"], ok=False)
        self.assertEqual(len(self.calls()), before)
        object_path.write_bytes(original_object)

        edited_comment = self.root / "edited-comment.md"
        edited_comment.write_text("The maintainer refined this closure comment.\n")
        edited = self.auto_close("edit", "--number", "1", "--checkpoint", selected["checkpoint"],
                                 "--comment-file", str(edited_comment), "--by", "maintainer")
        self.assertEqual(edited["inputs"], inputs)
        self.assertEqual(self.auto_close("review", "--number", "1")["plan"]["proposals"][0]["checkpoint"], edited["checkpoint"])
        self.assertEqual(len(self.calls()), before)

        path = self.root / "data/owner/repo/action-proposals/pr-1.json"
        legacy = json.loads(path.read_text())
        legacy.pop("checksum")
        legacy.pop("inputs")
        legacy["checksum"] = hashlib.sha256(json.dumps(legacy, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
        path.write_text(json.dumps(legacy) + "\n")
        self.auto_close("list", ok=False)
        self.auto_close("review", "--number", "1", ok=False)
        self.assertEqual(len(self.calls()), before)

    def test_rejection_retains_exact_proposal_and_never_publishes(self):
        ledger = self.root / "data/owner/repo/ledger.jsonl"
        ledger.write_text(json.dumps(dict(item(1, "An older fix", "pr"), reviewed=True, reviewed_by="maintainer")) + "\n")
        original = ledger.read_bytes()
        pending = self.propose(1, "--evidence-gap", "No selected cache snapshot")
        reviewed = self.auto_close("review", "--number", "1")
        self.assertNotIn("rejection", reviewed["plan"]["proposals"][0])
        self.auto_close("dismiss", "--number", "1", "--checkpoint", pending["checkpoint"])

        rejected = self.auto_close("reject", "--number", "1", "--checkpoint", pending["checkpoint"],
                                   "--by", "maintainer", "--reason", "Keep the compatibility work open")
        record = json.loads((self.root / "data/owner/repo/action-proposals/pr-1.json").read_text())
        self.assertEqual(rejected["status"], "rejected")
        self.assertFalse(rejected["active"])
        self.assertFalse(rejected["dismissed"])
        self.assertEqual(rejected["rejection"]["by"], "maintainer")
        self.assertEqual(rejected["rejection"]["reason"], "Keep the compatibility work open")
        self.assertEqual(rejected["rejection"]["proposal_checkpoint"], pending["checkpoint"])
        self.assertEqual(record["history"][-1]["checksum"], pending["checkpoint"])
        self.assertEqual(record["request_id"], record["history"][-1]["request_id"])
        self.assertEqual(self.auto_close("list")["rows"][0]["rejection"], rejected["rejection"])

        self.auto_close("review", "--number", "1", ok=False)
        self.auto_close("execute", "--number", "1", "--publish", "--approve", reviewed["approval"], ok=False)
        self.auto_close("reject", "--number", "1", "--checkpoint", pending["checkpoint"],
                        "--by", "maintainer", "--reason", "Stale retry", ok=False)
        self.auto_close("dismiss", "--number", "1", "--checkpoint", rejected["checkpoint"])
        self.assertTrue(self.auto_close("list")["rows"][0]["dismissed"])

        attempted = self.propose(2, "--evidence-gap", "No selected cache snapshot")
        saved = self.root / "data/owner/repo/action-proposals/pr-2.json"
        before = saved.read_bytes()
        writes = self.root / "data/owner/repo/writes"
        writes.mkdir()
        writes.joinpath(json.loads(before)["request_id"] + ".json").write_text("{}\n")
        self.auto_close("reject", "--number", "2", "--checkpoint", attempted["checkpoint"],
                        "--by", "maintainer", "--reason", "Too late", ok=False)
        self.assertEqual(saved.read_bytes(), before)

        optional = self.propose(3, "--evidence-gap", "No selected cache snapshot")
        no_reason = self.auto_close("reject", "--number", "3", "--checkpoint", optional["checkpoint"], "--by", "maintainer")
        self.assertEqual(no_reason["rejection"]["reason"], "")
        self.assertEqual(self.auto_close("list")["rows"][-1]["rejection"]["reason"], "")
        context = self.json_cli("item-context", "--expected-repo", "owner/repo", "read", "--kind", "pr", "--number", "3")
        self.assertEqual(next(row for row in context["rows"] if row["kind"] == "feedback")["fields"]["reason"]["preview"], "")
        self.assertEqual(ledger.read_bytes(), original)
        self.assertEqual(self.calls(), [])

    def test_group_handoff_reconsideration_retains_objection_and_attribution(self):
        ledger = self.root / "data/owner/repo/ledger.jsonl"
        ledger.write_text(json.dumps(dict(item(1, "An older fix", "pr"), reviewer_notes="Keep compatibility in view", reviewed=True)) +
                          "\n" + json.dumps(item(2, "Related report")) + "\n")
        original = ledger.read_bytes()
        group = self.json_cli("group", "create", "--title", "Compare fixes", "--by", "maintainer")
        self.run_cli("group", "add", group["id"], "--kind", "pr", "--number", "1", "--notes", "Check the old API", "--by", "maintainer")
        self.run_cli("group", "add", group["id"], "--kind", "issue", "--number", "2", "--by", "maintainer")

        def handoff():
            packet = self.json_cli("group", "export", group["id"], "--format", "json")
            return ("--group-id", group["id"], *[part for member in packet["items"] for part in
                                                 ("--member-context", f"{member['kind']}:{member['number']}:{member['local_context']['checkpoint']}")])

        initial = self.propose(1, *handoff(), "--evidence-gap", "No selected snapshot")
        shown = self.auto_close("context", "--number", "1", "--checkpoint", initial["checkpoint"])
        self.assertTrue(shown["current"])
        self.assertEqual(shown["item_context"]["rows"][0]["fields"]["reviewer_notes"]["preview"], "Keep compatibility in view")
        self.assertEqual(shown["requests"], 0)
        rejected = self.auto_close("reject", "--number", "1", "--checkpoint", initial["checkpoint"],
                                   "--by", "maintainer", "--reason", "Do not close until compatibility is resolved")
        rejected_view = self.auto_close("context", "--number", "1", "--checkpoint", rejected["checkpoint"])
        self.assertFalse(rejected_view["current"])
        self.assertEqual(rejected_view["latest_rejection"]["reason"], rejected["rejection"]["reason"])
        before = self.json_cli("item-context", "--expected-repo", "owner/repo", "read", "--kind", "pr", "--number", "1")
        self.assertEqual(next(row for row in before["rows"] if row["kind"] == "feedback")["fields"]["reason"]["preview"],
                         rejected["rejection"]["reason"])
        self.assertEqual(self.json_cli("group", "export", group["id"], "--format", "json")["items"][0]["local_context"]["feedback"][0]["reason"],
                         rejected["rejection"]["reason"])
        self.run_cli("group", "add", group["id"], "--kind", "pr", "--number", "1",
                     "--notes", "Old API no longer needed; closure may proceed", "--by", "maintainer")

        path = self.root / "data/owner/repo/action-proposals/pr-1.json"
        unchanged = path.read_bytes()
        self.propose(1, *handoff(), "--evidence-gap", "No selected snapshot", "--replace-checkpoint", rejected["checkpoint"], ok=False)
        self.propose(1, *handoff(), "--evidence-gap", "No selected snapshot", "--replace-checkpoint", initial["checkpoint"],
                     "--reconsideration-reason", "Maintainer resolved compatibility", ok=False)
        self.assertEqual(path.read_bytes(), unchanged)

        explanation = "Maintainer confirmed in the group that the old API is no longer needed"
        reconsidered = self.propose(1, *handoff(), "--evidence-gap", "No selected snapshot", "--replace-checkpoint", rejected["checkpoint"],
                                    "--reconsideration-reason", explanation)
        record = json.loads(path.read_text())
        self.assertEqual(reconsidered["reconsideration"]["by"], "agent:helper")
        self.assertEqual(reconsidered["reconsideration"]["reason"], explanation)
        self.assertEqual(reconsidered["reconsideration"]["rejected_checkpoint"], rejected["checkpoint"])
        self.assertEqual(record["history"][-1]["checksum"], rejected["checkpoint"])
        self.assertEqual(record["history"][-1]["rejection"]["reason"], rejected["rejection"]["reason"])
        self.assertNotEqual(record["request_id"], record["history"][-1]["request_id"])
        self.assertEqual(self.auto_close("review", "--number", "1")["plan"]["proposals"][0]["reconsideration"], reconsidered["reconsideration"])
        self.assertTrue(self.auto_close("context", "--number", "1", "--checkpoint", reconsidered["checkpoint"])["current"])
        self.assertEqual(self.json_cli("group", "export", group["id"], "--format", "json")["items"][0]["local_context"]["feedback"][0]["reason"],
                         rejected["rejection"]["reason"])

        edited = self.propose(1, *handoff(), "--evidence-gap", "No selected snapshot", "--replace-checkpoint", reconsidered["checkpoint"])
        self.assertEqual(edited["reconsideration"], reconsidered["reconsideration"])
        self.assertEqual(ledger.read_bytes(), original)
        self.assertEqual(self.calls(), [])

    def test_second_pass_reads_objection_and_uncertain_outcome_offline(self):
        ledger = self.root / "data/owner/repo/ledger.jsonl"
        ledger.write_text(json.dumps(item(1, "First PR", "pr")) + "\n" + json.dumps(item(2, "Second PR", "pr")) + "\n")
        original = ledger.read_bytes()
        group = self.json_cli("group", "create", "--title", "Choose a fix", "--description", "Compare both PRs", "--by", "maintainer")
        for number in (1, 2):
            self.run_cli("group", "add", group["id"], "--kind", "pr", "--number", str(number), "--by", "maintainer")

        def context(number, *args, ok=True):
            return self.json_cli("item-context", "--expected-repo", "owner/repo", "read", "--kind", "pr", "--number", str(number), *args, ok=ok)

        before = context(1)
        pending = self.propose(1, "--evidence-gap", "No selected cache snapshot")
        self.assertIsNone(context(1)["feedback_checkpoint"])
        self.assertEqual(context(1)["checkpoint"], before["checkpoint"])

        reason = "é" * 300
        self.auto_close("reject", "--number", "1", "--checkpoint", pending["checkpoint"], "--by", "maintainer", "--reason", reason)
        rejected = context(1)
        self.assertEqual(rejected["context_revision"], before["context_revision"])
        self.assertIsNotNone(rejected["feedback_checkpoint"])
        self.assertEqual(rejected["feedback_count"], 1)
        self.assertNotEqual(rejected["checkpoint"], before["checkpoint"])
        context(1, "--checkpoint", before["checkpoint"], ok=False)
        feedback = next(row for row in rejected["rows"] if row["kind"] == "feedback")
        self.assertEqual(feedback["fields"]["by"]["preview"], "maintainer")
        self.assertGreater(feedback["fields"]["reason"]["omitted_bytes"], 0)
        fragment = self.json_cli("item-context", "--expected-repo", "owner/repo", "source", "--kind", "pr", "--number", "1",
                                 "--row", feedback["id"], "--field", "reason", "--checkpoint", rejected["checkpoint"], "--max-bytes", "5")
        self.assertEqual(fragment["text"], "éé")

        self.propose(2, "--evidence-gap", "No selected cache snapshot")
        path = self.root / "data/owner/repo/action-proposals/pr-2.json"
        saved = json.loads(path.read_text())
        writes = self.root / "data/owner/repo/writes"
        writes.mkdir()
        writes.joinpath(saved["request_id"] + ".json").write_text("{}\n")
        attempted = context(2)
        self.assertEqual(attempted["feedback_count"], 1)
        self.assertEqual(next(row for row in attempted["rows"] if row["kind"] == "feedback")["fields"]["kind"]["preview"], "unreconciled_attempt")

        saved.pop("checksum")
        saved["status"] = "uncertain"
        saved["outcome"] = dict(request_id=saved["request_id"], comment=dict(status="succeeded", url="https://github.com/owner/repo/issues/2#issuecomment-5"),
                                state_change=dict(status="unknown", error="connection lost"))
        saved["checksum"] = hashlib.sha256(json.dumps(saved, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
        path.write_text(json.dumps(saved) + "\n")

        uncertain = context(2)
        self.assertEqual(uncertain["feedback_count"], 1)
        self.assertNotEqual(uncertain["feedback_checkpoint"], attempted["feedback_checkpoint"])
        outcome = next(row for row in uncertain["rows"] if row["kind"] == "feedback")
        self.assertEqual(outcome["fields"]["status"]["preview"], "uncertain")
        self.assertEqual(outcome["fields"]["comment_status"]["preview"], "succeeded")
        self.assertEqual(outcome["fields"]["state_status"]["preview"], "unknown")

        packet = self.json_cli("group", "export", group["id"], "--format", "json")
        by_number = {entry["number"]: entry["local_context"] for entry in packet["items"]}
        self.assertEqual(by_number[1]["feedback"][0]["reason"], reason)
        self.assertEqual(by_number[1]["feedback_checkpoint"], rejected["feedback_checkpoint"])
        self.assertEqual(by_number[2]["feedback"][0]["state_error"], "connection lost")
        markdown = self.run_cli("group", "export", group["id"]).stdout
        self.assertIn("Prior proposal feedback", markdown)
        self.assertIn("connection lost", markdown)
        self.assertEqual(ledger.read_bytes(), original)
        self.assertEqual(self.calls(), [])


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
