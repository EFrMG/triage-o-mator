"""Shared watch test fixtures."""

from .base import *

class WatchFixture(AcquisitionFixture):
    def setUp(self):
        super().setUp()
        self.responses["repos/owner/repo/pulls/1"] = dict(data=summary(state="closed", closed_at="2026-09-21T00:00:00Z"))
        self.timeline = "repos/owner/repo/issues/1/timeline?per_page=100&page=1"
        self.responses[self.timeline] = dict(data=[
            dict(id=99, event="closed", created_at="2026-09-21T00:00:00Z", actor=None),
            dict(comment(1), event="commented"),
            dict(event="committed", sha="b" * 40, committer=dict(date="2026-09-20T00:00:00Z")),
        ])

    def watch(self, command, *args, ok=True):
        (self.mock / "responses.json").write_text(json.dumps(self.responses))
        result = subprocess.run([str(self.root / "bin/cache"), command, "--number", "1", *args],
                                cwd=self.root, env=self.env, capture_output=True, text=True, timeout=20)
        if not ok:
            self.assertNotEqual(result.returncode, 0, result.stdout)
            return result.stderr

        self.assertEqual(result.returncode, 0, result.stderr)
        return json.loads(result.stdout)

    def enroll(self, *extra, budget="100"):
        snapshot = self.run_cache("fetch", "pr", "closure-watch", "--mode", "refresh", "--request-budget", budget)["snapshot_id"]

        return self.watch("watch-enroll", "--snapshot", snapshot, "--by", "operator", *extra)


CLAIM = dict(external=dict(namespace="runner", record_id="one"), actor=None, run_id=None, rationale="Original explanation", survivor=None, provenance="unknown", supports=[])


class ClosureStoreFixture(WatchFixture):
    def call(self, code, ok=True):
        script = self.root / "bin/closure-test"
        script.write_text("#!/usr/bin/env python3\nimport json\nfrom _cache import EvidenceCache\nfrom _evidence import repository\nfrom _closure_store import *\ncache = EvidenceCache(repository('owner/repo'))\n" + code)
        deny = self.root / "offline-bin"
        deny.mkdir(exist_ok=True)
        (deny / "gh").write_text("#!/bin/sh\necho offline-gh-denied >&2\nexit 99\n")
        (deny / "gh").chmod(0o755)
        env = dict(self.env, PATH=str(deny) + os.pathsep + self.env["PATH"])
        result = subprocess.run(["python3", str(script)], cwd=self.root, env=env, capture_output=True, text=True, timeout=30)
        if not ok:
            self.assertNotEqual(result.returncode, 0, result.stdout)
            return result.stderr
        self.assertEqual(result.returncode, 0, result.stderr)
        return json.loads(result.stdout)

    def capture(self, **changes):
        if changes:
            self.responses["repos/owner/repo/pulls/1"]["data"].update(changes)
        result = self.run_cache("fetch", "pr", "closure-watch", "--mode", "refresh", "--request-budget", "100")
        return result["snapshot_id"]

    def save(self, snapshot, claim=None, ok=True, **options):
        args = dict(event=99, comments=[1], **options)
        return self.call(f"print(json.dumps(import_closure(cache, 1, {snapshot!r}, 'operator', {claim or CLAIM!r}, **{args!r})))", ok=ok)


