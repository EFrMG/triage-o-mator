"""Agent-facing flow: agent_notes and proposed_by through bin/apply, bin/batch --diff, bin/read-batch, bin/next, bin/similar --query, and the maintainer sections of bin/report; in isolated checkouts with a fake gh."""

import csv
import json
import subprocess

from test_fetch_similar import CheckoutTest, item


class AgentFlowTests(CheckoutTest):
    def setUp(self):
        super().setUp()
        self.respond([item(1, "Suspend fails on lid close"), item(2, "Waybar crash on resume"), item(3, "Fix lid suspend handling", kind="pr")])
        self.run_cli("fetch")
        self.run_cli("sync")

    def decisions_path(self, batch_id):
        return self.root / f"data/owner/repo/batches/{batch_id}.decisions.jsonl"

    def make_batch(self, *args):
        out = self.run_cli("batch", *args)

        return next(line.split()[1].rstrip(":") for line in out.splitlines() if line.startswith("Batch "))

    def write_rows(self, path, rows):
        path.write_text("".join(json.dumps(r) + "\n" for r in rows))

    def test_agent_notes_and_attribution_round_trip(self):
        batch_id = self.make_batch("2", "--kind", "issue")
        rows = [json.loads(line) for line in self.decisions_path(batch_id).read_text().splitlines()]
        self.assertEqual(set(rows[0]), {"number", "kind", "category", "action", "confidence", "reason", "agent_notes", "proposed_by"})

        rows[0].update(category="bug", action="label-only", confidence="low", reason="Lid close skips suspend.", agent_notes="Reporter's logs show logind ignoring the lid.", proposed_by="agent:alice")
        rows[1].update(category="needs-info", action="comment-request-info", confidence="medium", reason="No logs.")
        self.write_rows(self.decisions_path(batch_id), rows)

        before = (self.root / "data/owner/repo/ledger.jsonl").read_text()
        self.assertIn("Would apply 2", self.run_cli("apply", str(self.decisions_path(batch_id)), "--only-untriaged", "--dry-run"))
        self.assertEqual((self.root / "data/owner/repo/ledger.jsonl").read_text(), before, "--dry-run must not write the ledger")

        self.run_cli("apply", str(self.decisions_path(batch_id)), "--only-untriaged")
        ledger = self.ledger()
        self.assertEqual(ledger[("issue", 1)]["triaged_by"], "agent:alice")
        self.assertEqual(ledger[("issue", 1)]["agent_notes"], "Reporter's logs show logind ignoring the lid.")
        self.assertEqual(ledger[("issue", 2)]["triaged_by"], "agent", "a row without proposed_by falls back to agent")
        self.assertFalse(ledger[("issue", 1)]["reviewed"])

        # A human's TUI-style save keeps the agent's evidence unless new notes are passed.
        self.run_cli("apply", "--number", "1", "--kind", "issue", "--category", "hardware-specific", "--action", "label-only", "--by", "bob")
        self.assertEqual(self.ledger()[("issue", 1)]["agent_notes"], "Reporter's logs show logind ignoring the lid.")
        self.assertEqual(self.ledger()[("issue", 1)]["triaged_by"], "bob")

        # Notes-only attaches a review without touching the decision or review state.
        self.run_cli("apply", "--number", "1", "--kind", "issue", "--agent-notes", "Code review: n/a")
        rec = self.ledger()[("issue", 1)]
        self.assertEqual((rec["category"], rec["triaged_by"], rec["agent_notes"]), ("hardware-specific", "bob", "Code review: n/a"))

        # agent_notes is exported for reading but never imported back.
        self.run_cli("export-csv")
        export = next((self.root / "data/owner/repo/exports").glob("ledger-*.csv"))
        with export.open(newline="") as f:
            table = list(csv.DictReader(f))

        for row in table:
            if row["number"] == "1":
                row["agent_notes"] = "edited in a spreadsheet"

        with export.open("w", newline="") as f:
            writer = csv.DictWriter(f, fieldnames=table[0].keys())
            writer.writeheader()
            writer.writerows(table)

        self.run_cli("import-csv", str(export), "--by", "carol")
        self.assertEqual(self.ledger()[("issue", 1)]["agent_notes"], "Code review: n/a")

    def test_only_the_reviewers_columns_come_back_from_a_spreadsheet(self):
        """README and AGENTS.md promise seven columns round-trip and that everything else in the CSV is a no-op, because the rest comes from GitHub via bin/sync."""
        self.run_cli("apply", "--number", "1", "--kind", "issue", "--category", "bug", "--action", "label-only", "--reason", "Proposed by an agent.", "--by", "agent:alice")

        self.run_cli("export-csv")
        export = next((self.root / "data/owner/repo/exports").glob("ledger-*.csv"))
        with export.open(newline="") as f:
            table = list(csv.DictReader(f))

        row = next(r for r in table if r["number"] == "1")
        row.update(
            category="hardware-specific",
            action="close-out-of-scope",
            confidence="high",
            reason="Checked the logs: NVIDIA only.",
            reviewed="true",
            reviewed_by="dana",
            reviewer_notes="Agreed with the agent, narrowed the category.",
            title="a title only GitHub decides",
            state="closed",
            labels="invented,labels",
            triaged_by="someone else",
        )

        with export.open("w", newline="") as f:
            writer = csv.DictWriter(f, fieldnames=table[0].keys())
            writer.writeheader()
            writer.writerows(table)

        self.run_cli("import-csv", str(export), "--by", "carol")
        rec = self.ledger()[("issue", 1)]

        self.assertEqual(
            (rec["category"], rec["action"], rec["confidence"], rec["reason"], rec["reviewed"], rec["reviewed_by"], rec["reviewer_notes"]),
            ("hardware-specific", "close-out-of-scope", "high", "Checked the logs: NVIDIA only.", True, "dana", "Agreed with the agent, narrowed the category."),
        )

        self.assertEqual((rec["title"], rec["state"], rec["labels"], rec["triaged_by"]), ("Suspend fails on lid close", "open", [], "agent:alice"), "what GitHub owns, and who proposed the decision, must survive a spreadsheet")

    def test_enrich_one_fetches_a_single_item(self):
        """What the TUI calls to load an item lazily, instead of pre-enriching a whole batch."""
        out = self.run_cli("enrich-one", "--kind", "issue", "--number", "1")
        item = json.loads(out[out.index("{"):out.rindex("}") + 1])
        self.assertEqual((item["number"], item["kind"]), (1, "issue"))
        self.assertIn("body", item)
        self.assertIn("comment_bodies", item)
        self.assertEqual(item["comment_authors"], ["commenter"])

    def test_batch_diff_and_read_batch(self):
        batch_id = self.make_batch("5", "--kind", "pr", "--diff")
        items = [json.loads(line) for line in (self.root / f"data/owner/repo/batches/{batch_id}.items.jsonl").read_text().splitlines()]
        self.assertTrue(all("diff_text" in i for i in items))

        listing = self.run_cli("read-batch", batch_id, "--list")
        self.assertIn("#3", listing)
        self.assertIn("(blank)", listing)
        text = self.run_cli("read-batch", batch_id, "--number", "3")
        self.assertIn("--- body", text)
        self.assertIn("--- diff", text)
        self.assertIn("an empty list proves nothing", text)

    def test_next_follows_the_batch_lifecycle(self):
        batch_id = self.make_batch("1", "--kind", "issue")
        suggestions = json.loads(self.run_cli("next", "--json"))["suggestions"]
        blank = [s for s in suggestions if batch_id in s["what"]]
        self.assertEqual((blank[0]["who"], blank[0]["do"].split()[0]), ("agent", "prompts/auto-triage.md"))

        rows = [json.loads(line) for line in self.decisions_path(batch_id).read_text().splitlines()]
        rows[0].update(category="bug", action="close-stale", confidence="low", reason="Quiet for a year.")
        self.write_rows(self.decisions_path(batch_id), rows)
        suggestions = json.loads(self.run_cli("next", "--json"))["suggestions"]
        self.assertTrue(any(s["who"] == "human" and batch_id in s["what"] and "proposals" in s["what"] for s in suggestions))

        self.run_cli("apply", str(self.decisions_path(batch_id)), "--only-untriaged")
        suggestions = json.loads(self.run_cli("next", "--json"))["suggestions"]
        self.assertTrue(any(s["who"] == "human" and "Delete finished batch" in s["what"] for s in suggestions))
        self.assertTrue(any(s["who"] == "human" and "1 would close something" in s["what"] for s in suggestions))

    def test_pr_without_code_review_is_suggested_for_review(self):
        self.run_cli("apply", "--number", "3", "--kind", "pr", "--category", "merge-ready", "--action", "approve-merge-candidate")
        self.assertIn("prompts/review-pr.md (e.g. #3)", self.run_cli("next"))
        self.run_cli("apply", "--number", "3", "--kind", "pr", "--agent-notes", "Code review by agent:alice")
        self.assertNotIn("prompts/review-pr.md", self.run_cli("next"))

    def test_query_ranks_issues_and_prs_together(self):
        found = json.loads(self.run_cli("similar", "--query", "lid suspend"))["candidates"]
        self.assertEqual({(c["kind"], c["number"]) for c in found}, {("issue", 1), ("pr", 3)})

    def test_report_puts_ready_groups_and_confirmed_decisions_first(self):
        self.run_cli("apply", "--number", "1", "--kind", "issue", "--category", "bug", "--action", "label-only", "--by", "bob", "--reviewed")
        self.run_cli("apply", "--number", "3", "--kind", "pr", "--category", "merge-ready", "--action", "approve-merge-candidate", "--agent-notes", "Diff read in full.")
        group = json.loads(self.run_cli("group", "create", "--title", "Decide the lid fix", "--description", "Decision needed: merge #3?", "--by", "agent:alice"))
        for kind, number in (("issue", 1), ("pr", 3)):
            self.run_cli("group", "add", group["id"], "--kind", kind, "--number", str(number), "--notes", "[context]", "--by", "agent:alice")

        self.run_cli("group", "update", group["id"], "--status", "ready", "--by", "bob")

        report = self.run_cli("report", "--stdout")
        sections = [line for line in report.splitlines() if line.startswith("## ")]
        self.assertEqual(sections[1:3], ["## Ready for maintainers (1 group)", "## Human-reviewed, ready to act (1)"])
        self.assertIn("1 unreviewed", report)
        self.assertIn("Fix lid suspend handling (open; `merge-ready/approve-merge-candidate`; not reviewed) [notes]: [context]", report)
        self.assertIn("| bob | 1 | 1 |", report)

    def test_group_export_has_no_carriage_returns_or_blank_line_runs(self):
        group = json.loads(self.run_cli("group", "create", "--title", "Lid", "--by", "alice"))
        self.run_cli("group", "add", group["id"], "--kind", "issue", "--number", "1", "--by", "alice")
        self.run_cli("group", "add", group["id"], "--kind", "pr", "--number", "3", "--notes", "Fix:\r\n\r\n\r\n\r\nworks\r\n", "--by", "alice")

        packet = self.run_cli("group", "export", group["id"], "--enrich")
        self.assertNotIn("\r", packet)
        self.assertNotIn("\n\n\n", packet)
        self.assertIn("Fix:\n\nworks", packet)

    def test_bulk_approve_and_batch_remove(self):
        for number, kind in ((1, "issue"), (3, "pr")):
            self.run_cli("apply", "--number", str(number), "--kind", kind, "--category", "bug" if kind == "issue" else "trivial", "--action", "label-only")

        self.run_cli("apply", "--approve", "--key", "issue:1", "--key", "pr:3", "--by", "bob")
        ledger = self.ledger()
        self.assertEqual([ledger[k]["reviewed_by"] for k in (("issue", 1), ("pr", 3))], ["bob", "bob"])
        self.assertFalse(ledger[("issue", 2)]["reviewed"])

        batch_id = self.make_batch("2", "--kind", "issue")
        self.assertIn("Removed 1 item(s)", self.run_cli("batch", "--remove", batch_id, "--key", "issue:2"))
        items = (self.root / f"data/owner/repo/batches/{batch_id}.items.jsonl").read_text()
        decisions = self.decisions_path(batch_id).read_text()
        self.assertNotIn('"number": 2,', items)
        self.assertNotIn('"number": 2,', decisions)

    def test_unapprove_and_clear_step_back_one_layer(self):
        self.run_cli("apply", "--number", "1", "--kind", "issue", "--category", "bug", "--action", "label-only", "--reason", "crash", "--by", "alice", "--reviewed")
        self.run_cli("apply", "--number", "1", "--kind", "issue", "--agent-notes", "evidence")

        out = self.run_cli("apply", "--unapprove", "--key", "issue:1", "--key", "issue:2")
        self.assertIn("Unapproved 1 decisions", out)
        self.assertIn("Skipped 1 not reviewed", out)
        row = self.ledger()[("issue", 1)]
        self.assertEqual((row["category"], row["reviewed"], row["reviewed_by"], row["reviewed_at"]), ("bug", False, "", ""))

        self.assertIn("Would clear 1", self.run_cli("apply", "--clear", "--number", "1", "--kind", "issue", "--dry-run"))
        self.assertEqual(self.ledger()[("issue", 1)]["category"], "bug")

        self.run_cli("apply", "--clear", "--number", "1", "--kind", "issue")
        row = self.ledger()[("issue", 1)]
        self.assertEqual([row[f] for f in ("category", "action", "reason", "triaged_by", "triaged_at", "batch_id")], [""] * 6)
        self.assertEqual(row["agent_notes"], "evidence")
        self.assertIn("Skipped 1 not triaged", self.run_cli("apply", "--clear", "--number", "1", "--kind", "issue"))

        mixed = subprocess.run([str(self.root / "bin/apply"), "--clear", "--number", "1", "--kind", "issue", "--category", "bug"], cwd=self.root, env=self.env, capture_output=True, text=True)
        self.assertNotEqual(mixed.returncode, 0)
        self.assertIn("only take --number/--kind or --key", mixed.stderr)

    def test_changing_an_approved_decision_resets_its_review(self):
        base = ("apply", "--number", "1", "--kind", "issue", "--category", "bug", "--action", "label-only", "--reason", "crash")
        self.run_cli(*base, "--by", "alice", "--reviewed")

        self.run_cli(*base, "--by", "bob")
        self.assertTrue(self.ledger()[("issue", 1)]["reviewed"], "the same decision again keeps the approval")

        self.run_cli("apply", "--number", "1", "--kind", "issue", "--category", "bug", "--action", "comment-request-info", "--reason", "crash", "--by", "bob")
        row = self.ledger()[("issue", 1)]
        self.assertEqual((row["action"], row["reviewed"], row["reviewed_by"]), ("comment-request-info", False, ""))
