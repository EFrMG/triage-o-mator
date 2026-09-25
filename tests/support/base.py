"""Shared checkout, GitHub, and comparison fixtures for Python and Go tests."""

import copy
import fcntl
import json
import os
import shutil
import subprocess
import sys
import tempfile
import unittest
from datetime import datetime, timedelta, timezone
from hashlib import sha256
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "bin"))
try:
    from _evidence import artifact_ref, object_name, repository, seal_snapshot
finally:
    sys.path.pop(0)

FAKE_GH = """#!/usr/bin/env python3
import json, os, sys
root = os.environ["FAKE_GH_DIR"]
with open(os.path.join(root, "gh_calls.log"), "a") as f:
    f.write(json.dumps(sys.argv[1:]) + "\\n")
if sys.argv[1] == "api":
    print(open(os.path.join(root, "gh_response.jsonl")).read(), end="")
else:
    print(json.dumps({"body": "Body", "comments": [{"body": "A comment", "author": {"login": "commenter"}, "createdAt": "2026-09-22T12:34:00Z"}]}))
"""


def item(number, title, state="open", kind="issue"):
    return {"number": number, "kind": kind, "title": title, "url": "", "author": "someone", "created_at": f"2025-01-{number:02d}T00:00:00Z", "updated_at": "", "state": state, "state_reason": None, "labels": [], "comments_count": 0}


class CheckoutTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        shutil.copytree(ROOT / "bin", self.root / "bin", ignore=shutil.ignore_patterns("triage-o-mator", "__pycache__"))
        shutil.copytree(ROOT / "config", self.root / "config")
        # What bin/install-to writes to mark an install; the scripts refuse to run anywhere else.
        (self.root / ".triage-install.json").write_text('{"tool": "%s"}\n' % ROOT)
        (self.root / "config/repo").write_text("owner/repo\n")
        (self.root / "data/owner/repo").mkdir(parents=True)
        mock = self.root / "mock"
        mock.mkdir()
        (mock / "gh").write_text(FAKE_GH)
        (mock / "gh").chmod(0o755)
        self.env = dict(os.environ, PATH=str(mock) + os.pathsep + os.environ["PATH"], FAKE_GH_DIR=str(mock))
        self.mock = mock

    def respond(self, items):
        (self.mock / "gh_response.jsonl").write_text("".join(json.dumps(i) + "\n" for i in items))

    def run_cli(self, command, *args):
        result = subprocess.run([str(self.root / "bin" / command), *args], cwd=self.root, env=self.env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)

        return result.stdout + result.stderr

    def last_endpoint(self):
        calls = [json.loads(l) for l in (self.mock / "gh_calls.log").read_text().splitlines()]

        return [c for c in calls if c[0] == "api"][-1][2]

    def meta(self):
        return json.loads((self.root / "data/owner/repo/raw/fetch_meta.json").read_text())

    def set_meta(self, **changes):
        meta = self.meta()
        meta.update(changes)
        (self.root / "data/owner/repo/raw/fetch_meta.json").write_text(json.dumps(meta))

    def ledger(self):
        path = self.root / "data/owner/repo/ledger.jsonl"

        return {(r["kind"], r["number"]): r for r in map(json.loads, path.read_text().splitlines())}


FAKE = '''#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
root = Path(os.environ["FAKE_GH_DIR"])
args = sys.argv[1:]
with (root / "gh_calls.log").open("a") as out:
    out.write(json.dumps(args) + "\\n")
assert args[0] == "api"
method = args[args.index("--method") + 1]
if args[-1] == "graphql":
    assert method == "POST" and "--input" in args
    payload = json.load(sys.stdin)
    from pathlib import Path
    sys.path.insert(0, str(root.parent / "bin"))
    from _acquire import CLOSING_QUERY
    assert payload["query"] == CLOSING_QUERY and "mutation" not in payload["query"]
    with (root / "graphql_calls.log").open("a") as out:
        out.write(json.dumps(payload) + "\\n")
else:
    assert method == "GET"
assert "--hostname" in args and "--include" in args
path = root / "responses.json"
responses = json.loads(path.read_text())
endpoint = args[-1]
if "Accept: application/vnd.github.diff" in args:
    endpoint += "#diff"
if endpoint not in responses:
    print("unexpected endpoint", file=sys.stderr)
    sys.exit(5)
response = responses[endpoint]
if isinstance(response, list):
    response = responses[endpoint].pop(0)
    path.write_text(json.dumps(responses))
status = response.get("status", 200)
print(f"HTTP/2.0 {status}")
for key, value in response.get("headers", {}).items():
    print(f"{key}: {value}")
print()
if "raw" in response:
    sys.stdout.flush()
    sys.stdout.buffer.write(response["raw"].encode("utf-8"))
else:
    print(json.dumps(response.get("data")))
sys.exit(response.get("exit_code", 0 if status == 200 else 1))
'''


def repo():
    return dict(full_name="owner/repo", id=42, node_id="R_repo")


def summary(kind="pr", **changes):
    data = dict(number=1, id=101, node_id="PR_1" if kind == "pr" else "I_1", html_url=f"https://github.com/owner/repo/{'pull' if kind == 'pr' else 'issues'}/1", state="open", title="A contribution", body="Body with evidence", updated_at="2026-09-22T01:00:00Z", comments=1)
    if kind == "pr":
        data.update(base=dict(sha="a" * 40, ref="main", repo=repo()), head=dict(sha="b" * 40, ref="fix", repo=repo()), changed_files=1, review_comments=1, merged=False, mergeable=True, additions=2, deletions=1, draft=False)

    data.update(changes)
    return data


def comment(number=1):
    return dict(id=number, node_id=f"C_{number}", html_url=f"https://github.com/owner/repo/issues/1#issuecomment-{number}", body=f"comment {number}", user=None, created_at="2026-09-22T00:00:00Z", updated_at="2026-09-22T00:00:00Z")


class AcquisitionFixture(CheckoutTest):
    def setUp(self):
        super().setUp()
        (self.mock / "gh").write_text(FAKE)
        self.responses = {
            "repos/owner/repo": dict(data=repo()),
            "repos/owner/repo/pulls/1": dict(data=summary()),
            "repos/owner/repo/issues/1": dict(data=summary("issue")),
            "repos/owner/repo/issues/1/comments?per_page=100&page=1": dict(data=[comment()]),
            "repos/owner/repo/pulls/1/files?per_page=100&page=1": dict(data=[dict(filename="new.py", previous_filename="old.py", status="renamed", patch="@@ -1 +1 @@\n-old\n+new")]),
            "repos/owner/repo/pulls/1/reviews?per_page=100&page=1": dict(data=[dict(id=9, state="CHANGES_REQUESTED", body="needs work", user=dict(login="reviewer"), submitted_at="2026-09-22T00:00:00Z", commit_id="b" * 40)]),
            "repos/owner/repo/pulls/1/comments?per_page=100&page=1": dict(data=[dict(comment(20), in_reply_to_id=19, pull_request_review_id=9, path="new.py", line=1, commit_id="b" * 40)]),
        }

    def run_cache(self, command="fetch", kind="pr", profile="pr-context", *extra, ok=True):
        (self.mock / "responses.json").write_text(json.dumps(self.responses))
        result = subprocess.run([str(self.root / "bin/cache"), command, "--kind", kind, "--number", "1", "--profile", profile, *extra], cwd=self.root, env=self.env, capture_output=True, text=True, timeout=20)
        if not ok:
            self.assertNotEqual(result.returncode, 0, result.stdout)
            return result.stderr

        self.assertEqual(result.returncode, 0, result.stderr)
        return json.loads(result.stdout)

    def calls(self):
        path = self.mock / "gh_calls.log"
        return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []


