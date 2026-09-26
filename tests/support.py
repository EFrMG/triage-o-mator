"""Disposable install and strict fake GitHub for the Python smoke suite."""

import json
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
FAKE_GH = '''#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
root = Path(os.environ["FAKE_GH_DIR"])
args = sys.argv[1:]
with (root / "calls.jsonl").open("a") as out:
    out.write(json.dumps(args) + "\\n")
if args[0] != "api":
    sys.exit("unexpected GitHub operation")
if "--paginate" in args:
    endpoint = args[args.index("--paginate") + 1]
    response = json.loads((root / "responses.json").read_text()).get(endpoint)
    if response is None:
        sys.exit("unexpected endpoint: " + endpoint)
    for row in response["data"]:
        print(json.dumps(row))
    sys.exit(0)
if args[args.index("--method") + 1] != "GET":
    sys.exit("unexpected GitHub operation")
endpoint = args[-1]
responses = json.loads((root / "responses.json").read_text())
if endpoint not in responses:
    sys.exit("unexpected endpoint: " + endpoint)
response = responses[endpoint]
print("HTTP/2.0 " + str(response.get("status", 200)))
print()
print(json.dumps(response.get("data")))
'''


def item(number, title, kind="issue"):
    return dict(number=number, kind=kind, title=title, url="", author="author", created_at="2025-01-01T00:00:00Z", updated_at="", state="open", state_reason=None, labels=[], comments_count=0)


def repository():
    return dict(full_name="owner/repo", id=42, node_id="R_repo")


def summary(kind="pr", number=1, **changes):
    value = dict(number=number, id=100 + number, node_id=f"{'PR' if kind == 'pr' else 'I'}_{number}", html_url=f"https://github.com/owner/repo/{'pull' if kind == 'pr' else 'issues'}/{number}", state="open", title="A contribution", body="Useful source text", updated_at="2026-09-22T01:00:00Z", comments=0)
    if kind == "pr":
        value.update(base=dict(sha="a" * 40, ref="main", repo=repository()), head=dict(sha="b" * 40, ref="fix", repo=repository()), changed_files=0, review_comments=0, merged=False, mergeable=True, additions=0, deletions=0, draft=False)

    value.update(changes)
    return value


class Workspace(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.root = Path(temporary.name)
        shutil.copytree(ROOT / "bin", self.root / "bin", ignore=shutil.ignore_patterns("triage-o-mator", "__pycache__"))
        shutil.copytree(ROOT / "config", self.root / "config")
        (self.root / ".triage-install.json").write_text(json.dumps(dict(tool=str(ROOT))))
        (self.root / "config/repo").write_text("owner/repo\n")
        (self.root / "data/owner/repo").mkdir(parents=True)
        self.mock = self.root / "mock"
        self.mock.mkdir()
        (self.mock / "gh").write_text(FAKE_GH)
        (self.mock / "gh").chmod(0o755)
        self.responses = {}
        self.env = dict(os.environ, PATH=str(self.mock) + os.pathsep + os.environ["PATH"], FAKE_GH_DIR=str(self.mock))
        self.save_responses()

    def save_responses(self):
        (self.mock / "responses.json").write_text(json.dumps(self.responses))

    def run_cli(self, command, *args, ok=True):
        self.save_responses()
        result = subprocess.run([str(self.root / "bin" / command), *args], cwd=self.root, env=self.env, capture_output=True, text=True, timeout=30)
        self.assertEqual(result.returncode == 0, ok, result.stderr + result.stdout)

        return result

    def json_cli(self, command, *args, ok=True):
        result = self.run_cli(command, *args, ok=ok)
        return json.loads(result.stdout) if result.stdout else None

    def calls(self):
        path = self.mock / "calls.jsonl"
        return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []

    def ledger(self):
        path = self.root / "data/owner/repo/ledger.jsonl"
        return {(row["kind"], row["number"]): row for row in map(json.loads, path.read_text().splitlines())}

    def seed_pr(self, state="open"):
        self.responses.update({
            "repos/owner/repo": dict(data=repository()),
            "repos/owner/repo/pulls/1": dict(data=summary(state=state, closed_at="2026-09-21T00:00:00Z" if state == "closed" else None)),
            "repos/owner/repo/issues/1/comments?per_page=100&page=1": dict(data=[]),
            "repos/owner/repo/pulls/1/files?per_page=100&page=1": dict(data=[]),
            "repos/owner/repo/pulls/1/reviews?per_page=100&page=1": dict(data=[]),
            "repos/owner/repo/pulls/1/comments?per_page=100&page=1": dict(data=[]),
            "repos/owner/repo/issues/1/timeline?per_page=100&page=1": dict(data=[dict(id=99, event="closed", created_at="2026-09-21T00:00:00Z")]),
        })
