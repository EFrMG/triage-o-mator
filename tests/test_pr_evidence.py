"""Read-only PR comparison evidence through fake REST and fixed-query GraphQL responses."""

import copy
import json


from support import AcquisitionFixture, DIFF, HEAD, PATCH, repo, summary


def linked(number=2, full_name="other/project"):
    return dict(id=f"I_{number}", number=number, url=f"https://github.com/{full_name}/issues/{number}", state="OPEN", updatedAt="2026-09-22T01:00:00Z", repository=dict(id="R_linked", nameWithOwner=full_name))


def closing(nodes=None, more=False, cursor=None, count=None):
    nodes = [linked()] if nodes is None else nodes
    return dict(data=dict(data=dict(node=dict(id="PR_1", number=1, url="https://github.com/owner/repo/pull/1", updatedAt="2026-09-22T01:00:00Z", baseRefOid="a" * 40, headRefOid=HEAD, repository=dict(id="R_repo", nameWithOwner="owner/repo"), closingIssuesReferences=dict(totalCount=len(nodes) if count is None else count, nodes=nodes, pageInfo=dict(hasNextPage=more, endCursor=cursor))), rateLimit=dict(remaining=5000, resetAt="2026-09-22T02:00:00Z"))))


class PRComparisonTests(AcquisitionFixture):
    def setUp(self):
        super().setUp()
        self.responses["repos/owner/repo/pulls/1/files?per_page=100&page=1"]["data"][0].update(patch=PATCH, additions=2, deletions=1)
        self.responses.update({
            "repos/owner/repo/pulls/1#diff": dict(raw=DIFF),
            f"repos/owner/repo/commits/{HEAD}/check-suites?per_page=100&page=1": dict(data=dict(total_count=1, check_suites=[dict(id=7, head_sha=HEAD, status="completed", conclusion="neutral")])),
            "repos/owner/repo/check-suites/7/check-runs?filter=all&per_page=100&page=1": dict(data=dict(total_count=1, check_runs=[dict(id=8, head_sha=HEAD, check_suite=dict(id=7), status="completed", conclusion="skipped", name="unit tests")])),
            f"repos/owner/repo/commits/{HEAD}/statuses?per_page=100&page=1": dict(data=[dict(id=9, state="pending", context="build", created_at="2026-09-22T00:59:00Z", updated_at="2026-09-22T01:00:00Z")]),
            "graphql": closing(),
        })

    def test_backlog_downloads_discussion_files_diff_and_closing_links_without_review_activity(self):
        result = self.run_cache("fetch", "pr", "backlog")
        self.assertEqual(set(result["components"]), {"summary", "comments", "files", "diff", "closing_issues"})
        self.assertFalse(any(result["problems"].values()))
        self.assertEqual(result["data"]["diff"], DIFF)
        self.assertEqual(result["data"]["closing_issues"][0]["identity"]["number"], 2)
        self.assertFalse(any("check-suites" in call[-1] or "/timeline" in call[-1] or "/reviews" in call[-1] for call in self.calls()))

        self.responses["repos/owner/repo/issues/1"] = dict(data=summary("issue"))
        result = self.run_cache("fetch", "issue", "backlog")
        self.assertFalse(any(result["problems"].values()))
        self.assertEqual(set(result["components"]), {"summary", "comments", "files", "diff", "closing_issues"})
        for name in ("files", "diff", "closing_issues"):
            self.assertEqual(result["components"][name]["status"], "not_applicable")

    def comparison(self, *extra):
        return self.run_cache("fetch", "pr", "pr-comparison", *extra)

    def test_comparison_retains_diff_checks_and_structured_cross_repo_links(self):
        result = self.comparison()
        self.assertFalse(any(result["problems"].values()))
        self.assertEqual(result["data"]["diff"], DIFF)
        self.assertEqual(result["components"]["diff"]["object"]["format"], "diff")
        checks = result["data"]["checks"]
        self.assertEqual([row["kind"] for row in checks], ["check_suite", "check_run", "status"])
        self.assertEqual([row["data"].get("conclusion", row["data"].get("state")) for row in checks], ["neutral", "skipped", "pending"])
        self.assertTrue(all(row["head_sha"] == HEAD for row in checks))
        self.assertEqual(result["data"]["closing_issues"][0]["repository"]["full_name"], "other/project")
        self.assertEqual(result["components"]["closing_issues"]["source"]["transport"], "graphql")
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())

    def test_offline_diff_enrichment_keeps_compatibility_without_fetches(self):
        self.comparison()
        count = len(self.calls())
        result = json.loads(self.run_cli("enrich-one", "--kind", "pr", "--number", "1", "--diff", "--cache-mode", "offline"))
        self.assertEqual(result["diff_text"], DIFF)
        self.assertFalse(result["evidence"]["problems"]["diff"])
        self.assertEqual(len(self.calls()), count)

    def test_offline_chunks_page_files_and_preserve_diff_lines(self):
        result = self.comparison()
        snapshot = result["snapshot_id"]
        before = len(self.calls())
        pieces, offset = [], 0
        while True:
            packet = json.loads(self.run_cli("cache", "chunk", "--snapshot", snapshot, "--kind", "pr", "--number", "1", "--component", "diff", "--offset", str(offset), "--limit", "2"))
            pieces.append(packet["text"])
            self.assertEqual(packet["descriptor"]["object"], result["components"]["diff"]["object"])
            if packet["continuation"] is None:
                break

            offset = packet["continuation"]["offset"]

        self.assertEqual("".join(pieces), DIFF)
        packet = json.loads(self.run_cli("cache", "chunk", "--snapshot", snapshot, "--kind", "pr", "--number", "1", "--component", "files", "--limit", "1"))
        self.assertEqual(json.loads(packet["text"]), result["data"]["files"][:1])
        self.assertEqual(len(self.calls()), before)

    def test_pr_code_profile_does_not_download_checks_or_graphql(self):
        result = self.run_cache("fetch", "pr", "pr-code")
        self.assertFalse(any(result["problems"].values()))
        self.assertFalse(any(call[-1] == "graphql" or "check-suites" in call[-1] for call in self.calls()))

    def test_cached_cli_diff_refresh_uses_code_profile(self):
        (self.mock / "responses.json").write_text(json.dumps(self.responses))
        result = json.loads(self.run_cli("enrich-one", "--kind", "pr", "--number", "1", "--diff", "--cache-mode", "refresh"))
        self.assertEqual(result["diff_text"], DIFF)
        self.assertFalse(any(result["evidence"]["problems"].values()))
        self.assertFalse(any(call[-1] == "graphql" for call in self.calls()))

    def test_comment_update_reuses_diff_but_new_head_refetches_it(self):
        first = self.run_cache("fetch", "pr", "pr-code")
        self.responses["repos/owner/repo/pulls/1"]["data"]["updated_at"] = "2026-09-22T01:01:00Z"
        # Force an acquisition for a missing component, then read the recorded newer summary through the code profile.
        self.run_cache("fetch", "pr", "pr-context")
        second = self.run_cache("fetch", "pr", "pr-code")
        self.assertEqual(second["components"]["diff"], first["components"]["diff"])
        before = sum("Accept: application/vnd.github.diff" in call for call in self.calls())
        self.responses["repos/owner/repo/pulls/1"]["data"]["head"]["sha"] = "c" * 40
        self.run_cache("fetch", "pr", "discussion", "--mode", "refresh")
        third = self.run_cache("fetch", "pr", "pr-code")
        self.assertEqual(third["components"]["diff"]["revision"]["head_sha"], "c" * 40)
        self.assertEqual(sum("Accept: application/vnd.github.diff" in call for call in self.calls()), before + 1)

    def test_checks_later_page_failure_and_budget_exhaustion_remain_partial(self):
        runs = self.responses["repos/owner/repo/check-suites/7/check-runs?filter=all&per_page=100&page=1"]
        runs["data"]["total_count"] = 2
        runs["headers"] = {"Link": '<ignored>; rel="next"'}
        self.responses["repos/owner/repo/check-suites/7/check-runs?filter=all&per_page=100&page=2"] = dict(status=503)
        result = self.comparison()
        self.assertEqual(result["components"]["checks"]["status"], "partial")
        self.assertTrue(any(row["kind"] == "check_run" for row in result["data"]["checks"]))
        self.assertEqual(result["components"]["closing_issues"]["status"], "complete")
        result = self.comparison("--mode", "refresh", "--request-budget", "8")
        self.assertEqual(result["stats"]["requests"], 8)
        self.assertIn("budget", result["components"]["checks"]["error"])
        self.assertIn("budget", result["components"]["closing_issues"]["error"])

    def test_truncated_or_wrong_diff_is_retained_but_partial(self):
        for diff in (DIFF.replace("+more\n", ""), DIFF.replace("b/new.py", "b/wrong.py"), DIFF.replace("+more", "+else"), ""):
            with self.subTest(diff=diff):
                self.responses["repos/owner/repo/pulls/1#diff"]["raw"] = diff
                result = self.run_cache("fetch", "pr", "pr-code", "--mode", "refresh")
                self.assertEqual(result["data"]["diff"], diff)
                self.assertEqual(result["components"]["diff"]["status"], "partial")

    def test_hunk_counts_are_checked_even_if_file_patch_matches(self):
        patch = PATCH.replace("@@ -1 +1,2 @@", "@@ -1 +1,3 @@")
        self.responses["repos/owner/repo/pulls/1#diff"]["raw"] = DIFF.replace(PATCH, patch)
        self.responses["repos/owner/repo/pulls/1/files?per_page=100&page=1"]["data"][0]["patch"] = patch
        result = self.run_cache("fetch", "pr", "pr-code")
        self.assertIn("truncated", result["components"]["diff"]["error"])

    def test_missing_patches_binary_submodules_and_unusual_paths_stay_partial(self):
        for diff in ("diff --git a/old.py b/new.py\nBinary files a/old.py and b/new.py differ\n", DIFF.replace("100644", "160000")):
            self.responses["repos/owner/repo/pulls/1#diff"]["raw"] = diff
            result = self.run_cache("fetch", "pr", "pr-code", "--mode", "refresh")
            self.assertIn("binary or submodule", result["components"]["diff"]["error"])

        file = self.responses["repos/owner/repo/pulls/1/files?per_page=100&page=1"]["data"][0]
        file["filename"] = "new file.py"
        self.assertIn("unusual", self.run_cache("fetch", "pr", "pr-code", "--mode", "refresh")["components"]["diff"]["error"])
        file.pop("patch")
        self.assertIn("complete files", self.run_cache("fetch", "pr", "pr-code", "--mode", "refresh")["components"]["diff"]["error"])

    def test_raw_diff_preserves_carriage_returns(self):
        diff = DIFF.replace("+more\n", "+more\r\n")
        self.responses["repos/owner/repo/pulls/1#diff"]["raw"] = diff
        result = self.run_cache("fetch", "pr", "pr-code")
        self.assertEqual(result["data"]["diff"], diff)
        self.assertEqual(result["components"]["diff"]["status"], "partial")
        self.assertEqual(self.run_cache("read", "pr", "pr-code")["data"]["diff"], diff)

    def test_unavailable_diff_is_not_fabricated_empty(self):
        self.responses["repos/owner/repo/pulls/1#diff"] = dict(status=404)
        result = self.run_cache("fetch", "pr", "pr-code")
        self.assertEqual(result["components"]["diff"]["status"], "unavailable")
        self.assertNotIn("diff", result["data"])

    def test_head_drift_invalidates_all_new_components(self):
        self.responses["repos/owner/repo/pulls/1"] = [dict(data=summary()), dict(data=summary(head=dict(sha="c" * 40, repo=repo())))]
        result = self.comparison()
        for name in ("diff", "checks", "closing_issues"):
            self.assertEqual(result["components"][name]["status"], "partial")

    def test_all_check_pages_and_historical_statuses_are_preserved(self):
        runs = self.responses["repos/owner/repo/check-suites/7/check-runs?filter=all&per_page=100&page=1"]
        runs["data"]["total_count"] = 2
        runs["headers"] = {"Link": '<ignored>; rel="next"'}
        second = copy.deepcopy(runs["data"]["check_runs"][0])
        second.update(id=10, conclusion="failure")
        self.responses["repos/owner/repo/check-suites/7/check-runs?filter=all&per_page=100&page=2"] = dict(data=dict(total_count=2, check_runs=[second]))
        result = self.comparison()
        self.assertFalse(result["problems"]["checks"])
        self.assertEqual([row["data"]["id"] for row in result["data"]["checks"]], [7, 8, 10, 9])

    def test_inaccessible_or_wrong_revision_checks_do_not_mean_passed(self):
        runs = "repos/owner/repo/check-suites/7/check-runs?filter=all&per_page=100&page=1"
        self.responses[runs]["data"]["check_runs"][0]["head_sha"] = "d" * 40
        result = self.comparison()
        self.assertIn("different head", result["components"]["checks"]["error"])
        self.responses[runs] = dict(status=404)
        result = self.comparison("--mode", "refresh")
        self.assertIn("HTTP 404", result["components"]["checks"]["error"])
        self.assertEqual(result["components"]["checks"]["status"], "partial")

    def test_missing_checks_are_verified_empty_not_an_aggregate_success(self):
        self.responses[f"repos/owner/repo/commits/{HEAD}/check-suites?per_page=100&page=1"]["data"] = dict(total_count=0, check_suites=[])
        self.responses[f"repos/owner/repo/commits/{HEAD}/statuses?per_page=100&page=1"]["data"] = []
        result = self.comparison()
        self.assertEqual(result["data"]["checks"], [])
        self.assertEqual(result["components"]["checks"]["status"], "complete")
        self.assertNotIn("passed", result)

    def test_fork_checks_have_separate_verified_repository_provenance(self):
        fork = dict(full_name="contributor/fork", id=88, node_id="R_fork")
        self.responses["repos/owner/repo/pulls/1"]["data"]["head"]["repo"] = fork
        self.responses["repos/contributor/fork"] = dict(data=fork)
        self.responses[f"repos/contributor/fork/commits/{HEAD}/check-suites?per_page=100&page=1"] = dict(data=dict(total_count=0, check_suites=[]))
        self.responses[f"repos/contributor/fork/commits/{HEAD}/statuses?per_page=100&page=1"] = dict(data=[dict(id=99, state="error", context="fork-build")])
        result = self.comparison()
        self.assertFalse(result["problems"]["checks"])
        self.assertEqual(result["data"]["checks"][-1]["repository"]["node_id"], "R_fork")
        self.responses["repos/contributor/fork"] = dict(status=404)
        self.assertIn("HTTP 404", self.comparison("--mode", "refresh")["components"]["checks"]["error"])

    def test_closing_links_paginate_with_cursor_variables(self):
        self.responses["graphql"] = [closing(more=True, cursor='cursor"with quote', count=2), closing(nodes=[linked(3)], count=2)]
        result = self.comparison()
        self.assertEqual([row["identity"]["number"] for row in result["data"]["closing_issues"]], [2, 3])
        calls = [json.loads(line) for line in (self.mock / "graphql_calls.log").read_text().splitlines()]
        self.assertEqual(calls[1]["variables"]["after"], 'cursor"with quote')
        self.assertEqual(calls[0]["query"], calls[1]["query"])

    def test_graphql_errors_nulls_and_partial_pages_are_not_empty_relationships(self):
        for response in (dict(data=dict(data=dict(node=None))), dict(data=dict(data=dict(node={}), errors=[dict(type="FORBIDDEN")]))):
            self.responses["graphql"] = response
            result = self.comparison("--mode", "refresh")
            self.assertTrue(result["problems"]["closing_issues"])

        self.responses["graphql"] = [closing(more=True, cursor="next", count=2), dict(data=dict(errors=[dict(type="FORBIDDEN")]))]
        result = self.comparison("--mode", "refresh")
        self.assertEqual(len(result["data"]["closing_issues"]), 1)
        self.assertEqual(result["components"]["closing_issues"]["status"], "partial")

    def test_graphql_identity_revision_and_repeated_cursor_fail_closed(self):
        for key, value in (("id", "PR_other"), ("headRefOid", "d" * 40)):
            response = closing()
            response["data"]["data"]["node"][key] = value
            self.responses["graphql"] = response
            self.assertTrue(self.comparison("--mode", "refresh")["problems"]["closing_issues"])

        self.responses["graphql"] = [closing(more=True, cursor="same", count=3), closing(nodes=[linked(3)], more=True, cursor="same", count=3)]
        self.assertIn("cursor repeated", self.comparison("--mode", "refresh")["components"]["closing_issues"]["error"])

    def test_graphql_rate_limit_stops_final_recheck(self):
        self.responses["graphql"] = dict(data=dict(errors=[dict(type="RATE_LIMITED")]), exit_code=1)
        result = self.comparison()
        self.assertEqual(result["components"]["summary"]["status"], "partial")
        self.assertEqual(self.calls()[-1][-1], "graphql")
        self.assertEqual(json.loads(self.run_cli("cache", "jobs"))["cooldown"]["reason"], "graphql-exhausted")

    def test_comparison_profile_is_not_applicable_to_issue_only_components(self):
        result = self.run_cache("fetch", "issue", "pr-comparison")
        for name in ("diff", "checks", "closing_issues"):
            self.assertEqual(result["components"][name]["status"], "not_applicable")

        self.assertFalse(any(call[-1] == "graphql" for call in self.calls()))
