"""An operator can hand a downloaded dataset to an agent without hidden GitHub reads."""

import json



from support import CorpusFixture
from support import AcquisitionFixture, summary
from support import DIFF, PATCH


class DatasetWorkflowTests(AcquisitionFixture):
    row = CorpusFixture.row
    cli = CorpusFixture.cli
    fetch_inventory = CorpusFixture.fetch_inventory
    create = CorpusFixture.create
    seed_details = CorpusFixture.seed_details
    status = CorpusFixture.status

    def test_download_handoff_compare_and_save_draft_offline(self):
        empty = json.loads(self.cli("cache", "handoff").stdout)
        self.assertIsNone(empty["corpus_id"])
        self.assertEqual(empty["requests"], 0)

        identifier, _ = self.create(profile="discussion")
        plan = self.status(identifier)["plan"]
        self.assertEqual(plan["max_age"], 86400)
        self.seed_details()
        for number in (1, 2):
            self.responses[f"repos/owner/repo/pulls/{number}"]["data"] = summary(
                number=number, id=100 + number, node_id=f"PR_{number}",
                html_url=f"https://github.com/owner/repo/pull/{number}",
                title="Fix a shared startup failure", comments=0)
        (self.mock / "responses.json").write_text(json.dumps(self.responses))
        acquired = json.loads(self.cli("cache", "corpus-run", identifier, "--request-budget", "100").stdout)
        self.assertEqual(acquired["counts"]["complete"], 2)

        before = len(self.calls())
        self.cli("cache", "select", identifier)
        handoff = json.loads(self.cli("cache", "handoff").stdout)
        self.assertEqual(handoff["corpus_id"], identifier)
        self.assertEqual(handoff["default_max_age"], 86400)
        self.assertIn("cache", handoff["local_path"])
        self.assertNotIn("Fix a shared startup failure", json.dumps(handoff), "handoff should remain metadata sized")
        self.assertIn(identifier, handoff["commands"]["candidates"])

        hits = json.loads(self.cli("cache", "search", "--corpus", identifier, "--component", "summary", "--query", "startup").stdout)
        self.assertEqual(len([row for row in hits["items"] if row["match"]]), 2)
        packet = json.loads(self.cli("cache", "candidates", "--corpus", identifier, "--compact").stdout)
        self.assertEqual(packet["candidate_sets"], 1)
        self.assertEqual(packet["observation_counts"]["examined"], 2)
        suggestion = packet["results"][0]
        self.assertEqual(suggestion["members"], [1, 2])
        self.assertEqual(suggestion["pairs"][0]["signals"][0]["signal"], "title")
        selected = acquired["progress"]["items"]["pr:1"]["snapshot_id"]
        chunk = json.loads(self.cli("cache", "chunk", "--snapshot", selected, "--kind", "pr", "--number", "1", "--component", "summary").stdout)
        self.assertTrue(chunk["available"])
        self.assertEqual(json.loads(chunk["text"])["title"], "Fix a shared startup failure")
        self.assertEqual(chunk["requests"], 0)

        path = self.root / "candidate-packet.json"
        path.write_text(json.dumps(packet))
        group = json.loads(self.cli("group", "create-candidate", "--file", str(path), "--candidate", suggestion["id"], "--by", "agent").stdout)
        self.assertEqual(group["status"], "draft")
        self.assertNotIn("reviewed", group)
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())
        self.assertEqual(len(self.calls()), before)

        old = json.loads(self.cli("cache", "candidates", "--corpus", identifier, "--as-of", "2099-01-01T00:00:00Z").stdout)
        self.assertEqual(old["candidate_sets"], 1)
        self.assertIn("discovery-evidence-stale:summary", old["results"][0]["holds"])
        self.assertEqual(len(self.calls()), before)

    def test_backlog_finds_pending_inventory_and_reads_selected_pr_code_offline(self):
        rows = [self.row("pr", number) for number in (1, 2, 3)]
        rows[2]["title"] = "Investigate the wake delay after suspend"
        rows[2]["body"] = "The resume path still misses applyWakePolicy when waking from sleep."
        self.fetch_inventory(rows)
        inventory = json.loads(self.cli("cache", "import-inventory").stdout)["snapshot_id"]
        created = json.loads(self.cli(
            "cache", "corpus-create", "--snapshot", inventory, "--scope", "open-prs", "--profile", "backlog"
        ).stdout)
        corpus_id = created["corpus_id"]

        shared_hunk = PATCH
        for number in (1, 2):
            self.responses[f"repos/owner/repo/pulls/{number}"] = dict(data=summary(
                number=number, id=100 + number, node_id=f"PR_{number}",
                html_url=f"https://github.com/owner/repo/pull/{number}", comments=0,
                title=f"Adjust wake behavior variant {number}", body=f"Variant {number} description"
            ))
            self.responses[f"repos/owner/repo/issues/{number}/comments?per_page=100&page=1"] = dict(data=[])
            self.responses[f"repos/owner/repo/pulls/{number}/files?per_page=100&page=1"] = dict(data=[
                dict(filename="new.py", previous_filename="old.py", status="renamed", patch=shared_hunk, additions=2, deletions=1)
            ])
            self.responses[f"repos/owner/repo/pulls/{number}#diff"] = dict(raw=DIFF)

        def closing_response(number):
            return dict(data=dict(data=dict(
                node=dict(id=f"PR_{number}", number=number, url=f"https://github.com/owner/repo/pull/{number}",
                          updatedAt="2026-09-22T01:00:00Z", baseRefOid="a" * 40, headRefOid="b" * 40,
                          repository=dict(id="R_repo", nameWithOwner="owner/repo"),
                          closingIssuesReferences=dict(totalCount=0, nodes=[], pageInfo=dict(hasNextPage=False, endCursor=None))),
                rateLimit=dict(remaining=5000, resetAt="2026-09-22T02:00:00Z"),
            )))

        self.responses["graphql"] = [closing_response(number) for number in (1, 2)]

        (self.mock / "responses.json").write_text(json.dumps(self.responses))
        acquired = json.loads(self.cli(
            "cache", "corpus-run", corpus_id, "--item-limit", "2", "--request-budget", "100"
        ).stdout)
        self.assertEqual(acquired["counts"]["complete"], 2, acquired["current_problems"])
        self.assertEqual(acquired["counts"]["pending"], 1)

        before_reads = len(self.calls())
        handoff = json.loads(self.cli("cache", "handoff", "--corpus", corpus_id).stdout)
        self.assertEqual(handoff["corpus_id"], corpus_id)
        all_inventory = json.loads(self.cli(
            "cache", "search", "--snapshot", inventory, "--component", "summary",
            "--query", "Investigate the wake delay", "--limit", "20"
        ).stdout)
        self.assertEqual([row["identity"]["number"] for row in all_inventory["items"] if row["match"]], [3])
        body_search = json.loads(self.cli(
            "cache", "search", "--snapshot", inventory, "--component", "summary",
            "--query", "applyWakePolicy", "--limit", "20"
        ).stdout)
        self.assertEqual([row["identity"]["number"] for row in body_search["items"] if row["match"]], [3])

        listing = json.loads(self.cli("cache", "corpus-list", corpus_id, "--limit", "20").stdout)
        self.assertEqual(listing["items"][2]["outcome"], "pending")
        snapshots = {row["identity"]["number"]: row["snapshot_id"] for row in listing["items"] if row["snapshot_id"]}
        files = json.loads(self.cli(
            "cache", "search", "--corpus", corpus_id, "--component", "files",
            "--query", "new.py", "--limit", "20"
        ).stdout)
        diffs = json.loads(self.cli(
            "cache", "search", "--corpus", corpus_id, "--component", "diff",
            "--query", "+more", "--limit", "20"
        ).stdout)
        self.assertEqual([row["identity"]["number"] for row in files["items"] if row["match"]], [1, 2])
        self.assertEqual([row["identity"]["number"] for row in diffs["items"] if row["match"]], [1, 2])

        for number in (1, 2):
            file_chunk = json.loads(self.cli(
                "cache", "chunk", "--snapshot", snapshots[number], "--kind", "pr", "--number", str(number),
                "--component", "files", "--limit", "20"
            ).stdout)
            self.assertEqual(json.loads(file_chunk["text"])[0]["filename"], "new.py")

            diff_parts = []
            offset = 0
            while True:
                chunk = json.loads(self.cli(
                    "cache", "chunk", "--snapshot", snapshots[number], "--kind", "pr", "--number", str(number),
                    "--component", "diff", "--offset", str(offset), "--limit", "2"
                ).stdout)
                diff_parts.append(chunk["text"])
                if chunk["continuation"] is None:
                    break

                offset = chunk["continuation"]["offset"]

            self.assertIn(shared_hunk, "".join(diff_parts))
            self.assertEqual(chunk["requests"], 0)

        self.assertEqual(len(self.calls()), before_reads)
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())
