"""Selected-item page resume and persistent throttle state in temporary fake-GitHub installs."""

import copy
import json


from support import EvidenceFixture
from support import AcquisitionFixture, comment, repo


class JobTests(AcquisitionFixture):
    python = EvidenceFixture.python

    def pages(self, name="files", count=4):
        endpoint = "repos/owner/repo/issues/1/comments" if name == "comments" else f"repos/owner/repo/pulls/1/{'comments' if name == 'review_comments' else name}"
        count_key = dict(files="changed_files", comments="comments", review_comments="review_comments").get(name)
        if count_key:
            self.responses["repos/owner/repo/pulls/1"]["data"][count_key] = count

        for page in range(1, count + 1):
            row = dict(filename=f"{page}.py", patch="patch") if name == "files" else comment(page)
            headers = dict(ETag=f'"{name}-{page}"')
            if page < count:
                headers["Link"] = '<ignored>; rel="next"'
            self.responses[f"{endpoint}?per_page=100&page={page}"] = dict(data=[row], headers=headers)

        return endpoint

    def job_path(self, name):
        return self.root / f"data/owner/repo/cache/jobs/pr-1-{name}.json"

    def interrupted(self, name="files"):
        endpoint = self.pages(name)
        last = f"{endpoint}?per_page=100&page=4"
        success = copy.deepcopy(self.responses[last])
        self.responses[last] = dict(status=503)
        first = self.run_cache("fetch", "pr", "pr-context", "--mode", "refresh")
        self.responses[last] = success
        self.assertEqual(first["components"][name]["status"], "partial")
        self.assertEqual(len(json.loads(self.job_path(name).read_text())["pages"]), 3)

        return endpoint, first

    def test_file_resume_skips_earlier_pages_and_preserves_partial_snapshot(self):
        endpoint, first = self.interrupted()
        before = len(self.calls())
        result = self.run_cache()
        endpoints = [call[-1] for call in self.calls()[before:]]
        self.assertNotIn(f"{endpoint}?per_page=100&page=1", endpoints)
        self.assertNotIn(f"{endpoint}?per_page=100&page=2", endpoints)
        self.assertIn(f"{endpoint}?per_page=100&page=3", endpoints)
        self.assertEqual(result["stats"]["resumed_pages"], 3)
        self.assertEqual(result["stats"]["requests"], 5)
        self.assertFalse(any(result["problems"].values()))
        self.assertEqual(json.loads(self.job_path("files").read_text())["state"], "complete")
        historical = json.loads(self.run_cli("cache", "show", first["snapshot_id"]))
        self.assertEqual(historical["items"][0]["components"]["files"]["status"], "partial")
        self.assertEqual(result["components"]["files"]["fetched_at"], first["components"]["files"]["fetched_at"])

    def test_mutable_pages_all_revalidate_with_conditional_get(self):
        endpoint, _ = self.interrupted("comments")
        for page in range(1, 4):
            self.responses[f"{endpoint}?per_page=100&page={page}"] = dict(status=304, raw="", exit_code=1)
        before = len(self.calls())
        result = self.run_cache()
        calls = [call for call in self.calls()[before:] if call[-1].startswith(endpoint)]
        self.assertEqual(len(calls), 4)
        for page, call in enumerate(calls[:3], 1):
            self.assertIn(f'If-None-Match: "comments-{page}"', call)
        self.assertEqual(result["stats"]["resumed_pages"], 3)
        self.assertEqual([row["id"] for row in result["data"]["comments"]], [1, 2, 3, 4])
        self.assertFalse(result["problems"]["comments"])

    def test_mutated_discussion_or_boundary_restarts_without_mixing_pages(self):
        for name in ("comments", "files", "reviews", "review_comments"):
            with self.subTest(name=name):
                endpoint, _ = self.interrupted(name)
                position = 3 if name == "files" else 1
                self.responses[f"{endpoint}?per_page=100&page={position}"]["data"][0]["patch" if name == "files" else "body"] = "changed"
                before = len(self.calls())
                result = self.run_cache()
                calls = [call[-1] for call in self.calls()[before:] if call[-1].startswith(endpoint)]
                self.assertIn(f"{endpoint}?per_page=100&page=1", calls)
                self.assertNotIn("resumed_pages", result["stats"])
                self.assertEqual(result["data"][name][position - 1]["patch" if name == "files" else "body"], "changed")
                self.assertFalse(result["problems"][name])

    def test_revision_count_scope_refresh_and_age_invalidate_saved_pages(self):
        endpoint, _ = self.interrupted()
        original = copy.deepcopy(self.responses["repos/owner/repo/pulls/1"]["data"])
        for change, extra in ((dict(updated_at="2026-09-22T02:00:00Z"), ()), (dict(changed_files=5), ()), (dict(head=dict(sha="c" * 40, repo=repo())), ()), (dict(head=dict(sha="b" * 40, repo=dict(full_name="owner/fork", id=43, node_id="R_fork"))), ()), ({}, ("--mode", "refresh")), ({}, ("--max-age", "0"))):
            with self.subTest(change=change, extra=extra):
                before = len(self.calls())
                self.responses["repos/owner/repo/pulls/1"]["data"] = dict(original, **change)
                result = self.run_cache("fetch", "pr", "pr-context", *extra)
                endpoints = [call[-1] for call in self.calls()[before:]]
                self.assertIn(f"{endpoint}?per_page=100&page=1", endpoints)
                self.assertNotIn("resumed_pages", result["stats"])
                self.responses["repos/owner/repo/pulls/1"]["data"] = copy.deepcopy(original)
                # Leave another unfinished job at the original revision for the next subcase.
                last = f"{endpoint}?per_page=100&page=4"
                success = self.responses[last]
                self.responses[last] = dict(status=503)
                self.run_cache("fetch", "pr", "pr-context", "--mode", "refresh")
                self.responses[last] = success

    def test_duplicate_failed_page_does_not_advance_checkpoint(self):
        endpoint, _ = self.interrupted("comments")
        self.responses[f"{endpoint}?per_page=100&page=4"]["data"] = [comment(3)]
        result = self.run_cache()
        self.assertIn("repeated", result["components"]["comments"]["error"])
        self.assertEqual(len(json.loads(self.job_path("comments").read_text())["pages"]), 3)
        self.assertEqual(len(result["data"]["comments"]), 3)

    def test_corrupt_job_or_page_is_refused_and_offline_evidence_still_reads(self):
        _, first = self.interrupted()
        path = self.job_path("files")
        original = path.read_text()
        value = json.loads(original)
        value["pages"][0]["number"] = 999
        path.write_text(json.dumps(value))
        self.assertIn("checksum", self.run_cache(ok=False))
        self.assertEqual(self.run_cache("read", "pr", "pr-context", "--snapshot", first["snapshot_id"])["snapshot_id"], first["snapshot_id"])
        path.write_text(original)
        ref = json.loads(original)["pages"][0]["object"]
        object_path = self.root / f"data/owner/repo/cache/objects/{ref['sha256']}.json"
        object_path.write_text("[]")
        self.assertIn("checksum", self.run_cache(ok=False))

    def test_cooldown_blocks_later_processes_refresh_and_other_items_but_not_offline(self):
        self.responses["repos/owner/repo/issues/1/comments?per_page=100&page=1"] = dict(status=429, headers={"Retry-After": "120"})
        first = self.run_cache()
        before = len(self.calls())
        self.assertIn("cooldown until", self.run_cache(ok=False))
        self.assertIn("cooldown until", self.run_cache("fetch", "pr", "pr-context", "--mode", "refresh", ok=False))
        self.assertIn("cooldown until", self.run_cache("fetch", "issue", "discussion", ok=False))
        self.assertEqual(len(self.calls()), before)
        self.assertEqual(self.run_cache("read")["snapshot_id"], first["snapshot_id"])
        jobs = json.loads(self.run_cli("cache", "jobs"))
        self.assertEqual(jobs["cooldown"]["reason"], "http-throttle-or-forbidden")
        self.assertEqual(len(self.calls()), before)

    def test_expired_cooldown_allows_retry_and_budgets_do_not_create_cooldown(self):
        self.run_cache("fetch", "pr", "pr-context", "--request-budget", "3")
        self.assertIsNone(json.loads(self.run_cli("cache", "jobs"))["cooldown"])
        self.python("""
from _cache import EvidenceCache
from _evidence import repository
from _jobs import Cooldown
cache = EvidenceCache(repository('owner/repo'))
Cooldown(cache).record('http-exhausted', '2020-01-01T00:00:00Z', {'x-ratelimit-remaining': '0', 'x-ratelimit-reset': '1577836900'})
""")
        self.assertFalse(any(self.run_cache()["problems"].values()))

    def test_cooldown_headers_fallback_backoff_and_identity_validation(self):
        self.run_cli("cache", "init")
        self.python("""
from _cache import EvidenceCache
from _evidence import repository
from _jobs import Cooldown
cache = EvidenceCache(repository('owner/repo'))
cooldown = Cooldown(cache)
cooldown.record('http-throttle-or-forbidden', '2026-09-22T00:00:00Z', {'retry-after': '120', 'x-ratelimit-remaining': '0', 'x-ratelimit-reset': '1790035500'})
assert cooldown.read()['retry_at'] == '2026-09-22T00:05:00+00:00'
cooldown.record('http-throttle-or-forbidden', '2026-09-22T00:05:00Z', {'retry-after': 'invalid'})
assert cooldown.read()['retry_at'] == '2026-09-22T00:07:00+00:00'
cooldown.record('graphql-exhausted', '2026-09-22T00:07:00Z', reset_at='2026-09-22T01:00:00Z')
assert cooldown.read()['retry_at'] == '2026-09-22T01:00:00+00:00'
assert cooldown.blocked('2026-09-22T00:59:59Z')
assert cooldown.blocked('2026-09-22T01:00:00Z') is None
cooldown.record('http-throttle-or-forbidden', '2026-09-23T00:00:00Z', {'retry-after': 'Wed, 23 Sep 2026 00:03:00 GMT'})
assert cooldown.read()['retry_at'] == '2026-09-23T00:03:00+00:00'
cache.identity = repository('owner/repo', host='elsewhere.example')
try:
    cooldown.read()
except ValueError:
    pass
else:
    raise AssertionError('foreign cooldown accepted')
""")

    def test_jobs_inspection_of_absent_cache_does_not_create_it(self):
        self.assertEqual(json.loads(self.run_cli("cache", "jobs"))["jobs"], [])
        self.assertFalse((self.root / "data/owner/repo/cache").exists())
        self.assertEqual(self.calls(), [])

    def test_killed_page_publication_resumes_only_durable_pages(self):
        endpoint = self.pages()
        (self.mock / "responses.json").write_text(json.dumps(self.responses))
        result = self.python("""
import json, os
from unittest.mock import patch
import _storage
from _reader import read_evidence
original = _storage.os.replace
def killed(source, target):
    original(source, target)
    if target.name == 'pr-1-files.json' and len(json.loads(target.read_text())['pages']) == 2:
        os._exit(73)
with patch.object(_storage.os, 'replace', killed):
    read_evidence('pr', 1, profile='pr-context', mode='refresh')
""", ok=False)
        self.assertEqual(result.returncode, 73)
        self.assertEqual(len(json.loads(self.job_path("files").read_text())["pages"]), 2)
        self.assertTrue(self.run_cache("read")["problems"]["files"])
        before = len(self.calls())
        result = self.run_cache()
        self.assertEqual(result["stats"]["resumed_pages"], 2)
        self.assertNotIn(f"{endpoint}?per_page=100&page=1", [call[-1] for call in self.calls()[before:]])
        self.assertFalse(any(result["problems"].values()))

    def test_failed_page_replace_or_flush_keeps_a_valid_checkpoint(self):
        endpoint = self.pages()
        (self.mock / "responses.json").write_text(json.dumps(self.responses))
        for after in (False, True):
            with self.subTest(after=after):
                failure = self.python(f"""
import json
from pathlib import Path
from unittest.mock import patch
import _storage
from _reader import read_evidence
original = _storage.os.replace
def failed(source, target):
    fault = target.name == 'pr-1-files.json' and len(json.loads(Path(source).read_text())['pages']) == 2
    if fault and not {after!r}:
        raise OSError('injected pre-replace failure')
    original(source, target)
    if fault:
        raise OSError('injected post-replace failure')
with patch.object(_storage.os, 'replace', failed):
    read_evidence('pr', 1, profile='pr-context', mode='refresh')
""", ok=False)
                self.assertIn("injected", failure.stderr)
                self.assertEqual(len(json.loads(self.job_path("files").read_text())["pages"]), 2 if after else 1)
                self.assertFalse(any(self.run_cache()["problems"].values()))

    def test_final_revision_drift_invalidates_job_before_next_resume(self):
        self.pages()
        first = copy.deepcopy(self.responses["repos/owner/repo/pulls/1"]["data"])
        moved = dict(first, head=dict(sha="c" * 40, repo=repo()))
        self.responses["repos/owner/repo/pulls/1"] = [dict(data=first), dict(data=moved)]
        result = self.run_cache()
        self.assertTrue(result["problems"]["files"])
        self.assertEqual(json.loads(self.job_path("files").read_text())["state"], "invalidated")
        self.responses["repos/owner/repo/pulls/1"] = dict(data=moved)
        result = self.run_cache()
        self.assertNotIn("resumed_pages", result["stats"])
        self.assertFalse(result["problems"]["files"])

    def test_terminal_page_is_rechecked_unconditionally_after_final_read_failure(self):
        endpoint = self.pages("reviews", 2)
        raw = copy.deepcopy(self.responses["repos/owner/repo/pulls/1"]["data"])
        self.responses["repos/owner/repo/pulls/1"] = [dict(data=raw), dict(status=503)]
        self.run_cache()
        # Force another review read while keeping the job's old revision/count valid; the summary cannot count submitted reviews.
        self.python("""
from _cache import EvidenceCache
from _evidence import repository, seal_snapshot
cache = EvidenceCache(repository('owner/repo'))
manifest, _ = cache.latest('pr', 1)
manifest['items'][0]['components']['reviews'].update(status='partial', error='revalidation needed')
from _acquire import now
manifest['completed_at'] = now()
cache.publish(seal_snapshot(manifest), {})
""")
        self.responses["repos/owner/repo/pulls/1"] = dict(data=raw)
        self.responses[f"{endpoint}?per_page=100&page=2"]["headers"]["Link"] = '<ignored>; rel="next"'
        self.responses[f"{endpoint}?per_page=100&page=3"] = dict(data=[comment(3)])
        before = len(self.calls())
        result = self.run_cache()
        terminal_calls = [call for call in self.calls()[before:] if call[-1] == f"{endpoint}?per_page=100&page=2"]
        self.assertTrue(terminal_calls)
        self.assertFalse(any(any(arg.startswith("If-None-Match:") for arg in call) for call in terminal_calls))
        self.assertEqual(len(result["data"]["reviews"]), 3)

    def test_null_page_is_not_a_conditional_cache_hit(self):
        endpoint, _ = self.interrupted("comments")
        self.responses[f"{endpoint}?per_page=100&page=1"] = dict(data=None)
        result = self.run_cache()
        self.assertNotIn("resumed_pages", result["stats"])
        self.assertTrue(result["problems"]["comments"])

    def test_valid_checksum_does_not_bypass_job_contracts_or_symlink_checks(self):
        self.interrupted()
        self.python("""
import copy, json
from _jobs import sealed
from _cache import EvidenceCache
from _evidence import repository
from _reader import read_evidence
cache = EvidenceCache(repository('owner/repo'))
path = cache.path('jobs', 'pr-1-files.json')
original = json.loads(path.read_text())
original.pop('checksum')
changes = [dict(schema_version=2), dict(resource='https://evil.invalid'), dict(repository=repository('other/repo')), dict(scope=[])]
for change in changes:
    path.write_text(json.dumps(sealed(dict(original, **change))))
    try:
        read_evidence('pr', 1, profile='pr-context', mode='refresh')
    except ValueError:
        pass
    else:
        raise AssertionError('invalid job accepted')
outside = cache.root.parent / 'keep.txt'
outside.write_text('preserve')
path.unlink()
path.symlink_to(outside)
try:
    read_evidence('pr', 1, profile='pr-context', mode='refresh')
except ValueError:
    pass
else:
    raise AssertionError('job symlink followed')
assert outside.read_text() == 'preserve'
""")

    def test_successful_page_with_exhausted_limit_is_saved_but_run_stops(self):
        endpoint = self.pages("comments")
        self.responses[f"{endpoint}?per_page=100&page=1"]["headers"]["X-RateLimit-Remaining"] = "0"
        result = self.run_cache()
        self.assertEqual(result["stats"]["requests"], 3)
        self.assertEqual(result["components"]["comments"]["received_count"], 1)
        self.assertEqual(len(json.loads(self.job_path("comments").read_text())["pages"]), 1)
        self.assertEqual(json.loads(self.run_cli("cache", "jobs"))["cooldown"]["reason"], "http-exhausted")

    def test_resume_boundary_budget_failure_preserves_checkpoint(self):
        self.interrupted("comments")
        before = self.job_path("comments").read_bytes()
        result = self.run_cache("fetch", "pr", "pr-context", "--request-budget", "3")
        self.assertEqual(result["stats"]["requests"], 3)
        self.assertTrue(result["problems"]["comments"])
        self.assertEqual(self.job_path("comments").read_bytes(), before)
        self.assertFalse(any(self.run_cache()["problems"].values()))
