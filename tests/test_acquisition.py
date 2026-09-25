"""Selected-item evidence acquisition with a strict fake GitHub REST server; never calls the network."""

import json
import subprocess


from support import AcquisitionFixture, CheckoutTest, comment, item, repo, summary


class AcquisitionTests(AcquisitionFixture):
    def test_profiles_keep_source_details_and_no_ledger_changes(self):
        result = self.run_cache()
        self.assertFalse(any(result["problems"].values()))
        self.assertEqual(result["repository"]["database_id"], 42)
        self.assertEqual(result["data"]["files"][0]["previous_filename"], "old.py")
        self.assertEqual(result["data"]["review_comments"][0]["in_reply_to_id"], 19)
        self.assertIsNone(result["data"]["comments"][0]["user"])
        self.assertEqual(result["data"]["reviews"][0]["state"], "CHANGES_REQUESTED")
        self.assertEqual(result["stats"]["requests"], 7)
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())
        self.assertTrue(all(call[call.index("--hostname") + 1] == "github.com" for call in self.calls()))

    def test_offline_missing_does_not_initialize_or_call_github(self):
        result = self.run_cache("read")
        self.assertIsNone(result["snapshot_id"])
        self.assertEqual(result["problems"]["summary"], ["missing"])
        self.assertEqual(self.calls(), [])
        self.assertFalse((self.root / "data/owner/repo/cache").exists())

    def test_cache_hit_offline_and_explicit_refresh(self):
        first = self.run_cache()
        count = len(self.calls())
        self.assertEqual(self.run_cache()["snapshot_id"], first["snapshot_id"])
        self.assertEqual(self.run_cache("read")["snapshot_id"], first["snapshot_id"])
        self.assertEqual(len(self.calls()), count)
        refreshed = self.run_cache("fetch", "pr", "pr-context", "--mode", "refresh")
        self.assertNotEqual(refreshed["snapshot_id"], first["snapshot_id"])
        self.assertEqual(len(self.calls()), count * 2)

    def test_pagination_is_separate_and_preserves_all_comments(self):
        self.responses["repos/owner/repo/pulls/1"]["data"]["comments"] = 2
        self.responses["repos/owner/repo/issues/1/comments?per_page=100&page=1"]["headers"] = {"Link": '<https://api.github.com/repos/owner/repo/issues/1/comments?page=2>; rel="next"'}
        self.responses["repos/owner/repo/issues/1/comments?per_page=100&page=2"] = dict(data=[comment(2)])
        result = self.run_cache()
        self.assertEqual(result["components"]["comments"]["received_count"], 2)
        self.assertTrue(result["components"]["comments"]["pagination_complete"])
        self.assertFalse(result["problems"]["comments"])

    def test_later_page_failure_is_partial_and_resume_reuses_other_components(self):
        self.responses["repos/owner/repo/pulls/1"]["data"]["comments"] = 2
        page = self.responses["repos/owner/repo/issues/1/comments?per_page=100&page=1"]
        page["headers"] = {"Link": '<ignored>; rel="next"'}
        self.responses["repos/owner/repo/issues/1/comments?per_page=100&page=2"] = dict(status=503)
        first = self.run_cache()
        self.assertEqual(first["components"]["comments"]["status"], "partial")
        self.assertEqual(len(first["data"]["comments"]), 1)
        self.responses["repos/owner/repo/issues/1/comments?per_page=100&page=2"] = dict(data=[comment(2)])
        resumed = self.run_cache()
        self.assertEqual(resumed["stats"]["cache_hits"], 3)
        self.assertFalse(any(resumed["problems"].values()))
        self.assertEqual(json.loads(self.run_cli("cache", "show", first["snapshot_id"]))["items"][0]["components"]["comments"]["status"], "partial")

    def test_request_budget_stops_and_completed_components_resume(self):
        first = self.run_cache("fetch", "pr", "pr-context", "--request-budget", "3")
        self.assertEqual(first["stats"]["requests"], 3)
        self.assertIn("budget", first["components"]["files"]["error"])
        self.assertEqual(first["components"]["summary"]["status"], "partial")
        resumed = self.run_cache()
        self.assertEqual(resumed["stats"]["cache_hits"], 1)
        self.assertFalse(any(resumed["problems"].values()))

    def test_rate_limit_stops_entire_run_without_retry(self):
        self.responses["repos/owner/repo/issues/1/comments?per_page=100&page=1"] = dict(status=429, headers={"Retry-After": "60"})
        result = self.run_cache()
        self.assertEqual(result["stats"]["requests"], 3)
        self.assertIn("retry-after=60", result["components"]["comments"]["error"])
        self.assertEqual(result["components"]["summary"]["status"], "partial")

    def test_revision_drift_cannot_claim_complete_code_evidence(self):
        moved = summary(head=dict(sha="c" * 40, repo=repo()))
        self.responses["repos/owner/repo/pulls/1"] = [dict(data=summary()), dict(data=moved)]
        result = self.run_cache()
        self.assertEqual(result["revision"]["head_sha"], "c" * 40)
        self.assertEqual(result["components"]["files"]["status"], "partial")
        self.assertIn("head_sha changed or unknown", result["problems"]["files"])

    def test_repository_or_item_identity_change_is_refused(self):
        self.run_cache()
        self.responses["repos/owner/repo"]["data"]["id"] = 777
        self.assertIn("repository database_id mismatch", self.run_cache("fetch", "pr", "pr-context", "--mode", "refresh", ok=False))
        self.responses["repos/owner/repo"]["data"] = repo()
        self.responses["repos/owner/repo/pulls/1"]["data"]["id"] = 222
        self.assertIn("item database_id mismatch", self.run_cache("fetch", "pr", "pr-context", "--mode", "refresh", ok=False))

    def test_wrong_kind_and_redirected_item_are_refused(self):
        self.responses["repos/owner/repo/issues/1"]["data"]["pull_request"] = {}
        self.assertIn("URL/kind", self.run_cache("fetch", "issue", "discussion", ok=False))
        self.responses["repos/owner/repo/pulls/1"]["data"]["html_url"] = "https://github.com/elsewhere/repo/pull/1"
        self.assertIn("URL/kind", self.run_cache(ok=False))

    def test_unavailable_collection_is_not_verified_empty(self):
        self.responses["repos/owner/repo/pulls/1/reviews?per_page=100&page=1"] = dict(status=404)
        result = self.run_cache()
        self.assertEqual(result["components"]["reviews"]["status"], "unavailable")
        self.responses["repos/owner/repo/pulls/1/reviews?per_page=100&page=1"] = dict(data=[])
        result = self.run_cache()
        self.assertEqual(result["components"]["reviews"]["status"], "complete")
        self.assertEqual(result["data"]["reviews"], [])

    def test_issue_pr_components_are_not_applicable_and_merged_is_explicit(self):
        issue = self.run_cache("fetch", "issue")
        self.assertEqual(issue["components"]["files"]["status"], "not_applicable")
        self.assertFalse(any("/pulls/" in call[-1] for call in self.calls()))
        self.responses["repos/owner/repo/pulls/1"]["data"].update(state="closed", merged=True)
        self.assertEqual(self.run_cache()["data"]["summary"]["state"], "merged")

    def test_missing_patches_and_capped_files_are_evidence_gaps(self):
        files = self.responses["repos/owner/repo/pulls/1/files?per_page=100&page=1"]
        files["data"][0].pop("patch")
        self.assertIn("patches are omitted", self.run_cache()["components"]["files"]["error"])
        self.responses["repos/owner/repo/pulls/1"]["data"]["changed_files"] = 3001
        for page in range(1, 31):
            self.responses[f"repos/owner/repo/pulls/1/files?per_page=100&page={page}"] = dict(data=[dict(filename=f"{i}.py", patch="patch") for i in range((page - 1) * 100, page * 100)], headers={"Link": '<ignored>; rel="next"'} if page < 30 else {})
        result = self.run_cache("fetch", "pr", "pr-context", "--mode", "refresh")
        self.assertTrue(result["components"]["files"]["truncated"])
        self.assertIn("3000", result["components"]["files"]["error"])
        before = len(self.calls())
        resumed = self.run_cache()
        self.assertTrue(resumed["components"]["files"]["truncated"])
        self.assertFalse(any(call[-1].endswith("page=31") for call in self.calls()[before:]))

    def test_offline_staleness_is_visible_and_enrichment_keeps_compatibility_fields(self):
        self.run_cache()
        count = len(self.calls())
        stale = self.run_cache("read", "pr", "discussion", "--max-age", "0")
        self.assertIn("observation outside freshness window", stale["problems"]["summary"])
        result = json.loads(self.run_cli("enrich-one", "--kind", "pr", "--number", "1", "--cache-mode", "offline"))
        self.assertEqual(result["body"], "Body with evidence")
        self.assertEqual(result["comment_authors"], [""])
        self.assertEqual(result["comment_bodies"], ["comment 1"])
        self.assertEqual(result["comment_dates"], [comment()["created_at"]])
        self.assertEqual(result["mergeable"], "MERGEABLE")
        self.assertIn("evidence", result)
        self.assertEqual(len(self.calls()), count)

    def test_narrower_refresh_keeps_old_components_and_snapshots(self):
        first = self.run_cache()
        narrowed = self.run_cache("fetch", "pr", "discussion", "--mode", "refresh")
        self.assertEqual(narrowed["stats"]["requests"], 4)
        full = self.run_cache("read")
        self.assertEqual(full["components"]["files"], first["components"]["files"])
        self.assertEqual(full["snapshot_id"], narrowed["snapshot_id"])

    def test_diff_cache_missing_is_reported_without_network_call(self):
        result = subprocess.run([str(self.root / "bin/enrich-one"), "--kind", "pr", "--number", "1", "--cache-mode", "offline", "--diff"], cwd=self.root, env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        data = json.loads(result.stdout)
        self.assertEqual(data["evidence"]["problems"]["diff"], ["missing"])
        self.assertNotIn("diff_text", data)
        self.assertEqual(self.calls(), [])

    def test_repeated_page_entries_and_count_mismatch_are_not_complete(self):
        page = self.responses["repos/owner/repo/issues/1/comments?per_page=100&page=1"]
        page["data"] = [comment(), comment()]
        result = self.run_cache()
        self.assertIn("repeated", result["components"]["comments"]["error"])
        page["data"] = []
        result = self.run_cache()
        self.assertIn("count differs", result["components"]["comments"]["error"])

    def test_final_recheck_failure_keeps_checkpoint_and_previous_snapshot(self):
        first = self.run_cache()
        self.responses["repos/owner/repo/pulls/1"] = [dict(data=summary()), dict(status=404)]
        failed = self.run_cache("fetch", "pr", "pr-context", "--mode", "refresh")
        self.assertEqual(failed["components"]["summary"]["status"], "partial")
        self.assertEqual(failed["data"]["summary"]["state"], "open")
        self.assertIn("HTTP 404", failed["components"]["summary"]["error"])
        self.assertEqual(self.run_cache("read")["snapshot_id"], failed["snapshot_id"])
        self.assertEqual(json.loads(self.run_cli("cache", "show", first["snapshot_id"]))["snapshot_id"], first["snapshot_id"])

    def test_initial_unavailability_does_not_fabricate_closed_state(self):
        self.responses["repos/owner/repo/pulls/1"] = dict(status=404)
        self.assertIn("HTTP 404", self.run_cache(ok=False))
        self.assertIsNone(self.run_cache("read")["snapshot_id"])

    def test_count_drift_without_timestamp_change_is_partial(self):
        self.responses["repos/owner/repo/pulls/1"] = [dict(data=summary()), dict(data=summary(comments=2))]
        result = self.run_cache()
        self.assertEqual(result["components"]["comments"]["status"], "partial")
        self.assertIn("counts changed", result["components"]["comments"]["error"])

    def test_reuse_checks_new_counts_even_when_revision_timestamp_is_unchanged(self):
        files = self.responses["repos/owner/repo/pulls/1/files?per_page=100&page=1"]["data"]
        files[0].pop("patch")
        self.run_cache()
        files[0]["patch"] = "patch"
        self.responses["repos/owner/repo/pulls/1"]["data"]["comments"] = 2
        self.responses["repos/owner/repo/issues/1/comments?per_page=100&page=1"]["data"].append(comment(2))
        result = self.run_cache()
        self.assertEqual(result["components"]["comments"]["received_count"], 2)
        self.assertEqual(result["stats"]["cache_hits"], 2)

    def test_explicit_host_is_used_and_conflicting_cache_host_is_refused(self):
        self.responses["repos/owner/repo/issues/1"]["data"]["html_url"] = "https://git.example/owner/repo/issues/1"
        (self.mock / "responses.json").write_text(json.dumps(self.responses))
        result = json.loads(self.run_cli("cache", "--host", "git.example", "fetch", "--kind", "issue", "--number", "1"))
        self.assertEqual(result["repository"]["host"], "git.example")
        self.assertTrue(all(call[call.index("--hostname") + 1] == "git.example" for call in self.calls()))
        count = len(self.calls())
        self.assertIn("host mismatch", self.run_cache(ok=False))
        self.assertEqual(len(self.calls()), count)

    def test_concurrent_cache_preferred_reads_share_acquisition(self):
        (self.mock / "responses.json").write_text(json.dumps(self.responses))
        command = [str(self.root / "bin/cache"), "fetch", "--kind", "pr", "--number", "1", "--profile", "pr-context"]
        workers = []
        try:
            for _ in range(2):
                workers.append(subprocess.Popen(command, cwd=self.root, env=self.env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True))

            results = []
            for worker in workers:
                out, err = worker.communicate(timeout=20)
                self.assertEqual(worker.returncode, 0, err)
                results.append(json.loads(out))

            self.assertEqual(results[0]["snapshot_id"], results[1]["snapshot_id"])
            self.assertEqual(len(self.calls()), 7)
        finally:
            for worker in workers:
                if worker.poll() is None:
                    worker.kill()

                worker.communicate()


class InventoryBodyTests(CheckoutTest):
    def test_inventory_body_is_retained_but_not_added_to_ledger(self):
        self.respond([dict(item(1, "item"), body="already fetched body")])
        self.run_cli("fetch")
        self.run_cli("sync")
        raw = self.root / "data/owner/repo/raw/issues_and_prs.jsonl"
        self.assertEqual(json.loads(raw.read_text())["body"], "already fetched body")
        self.assertNotIn("body", self.ledger()[("issue", 1)])
        calls = [json.loads(line) for line in (self.mock / "gh_calls.log").read_text().splitlines()]
        self.assertIn("body", calls[0][calls[0].index("--jq") + 1])
