"""Only comment-plus can publish, and its approval binds the exact operation."""

import json

from support import Workspace


WRITE_GH = '''#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
root = Path(os.environ["FAKE_GH_DIR"])
args = sys.argv[1:]
method = args[args.index("--method") + 1]
payload = json.load(sys.stdin) if method != "GET" else None
with (root / "write-calls.jsonl").open("a") as out:
    out.write(json.dumps(dict(method=method, body=payload, endpoint=args[-3] if method != "GET" else args[-1])) + "\\n")
if method == "GET":
    print((root / "item.json").read_text())
elif method == "POST":
    print(json.dumps(dict(id=42, html_url="https://github.com/owner/repo/issues/1#issuecomment-42")))
elif method == "PATCH":
    if (root / "fail-patch").exists():
        sys.exit("connection lost")
    item = json.loads((root / "item.json").read_text())
    item["state"] = payload["state"]
    print(json.dumps(item))
else:
    sys.exit("unexpected operation")
'''


class WriteTests(Workspace):
    def setUp(self):
        super().setUp()
        (self.mock / "gh").write_text(WRITE_GH)
        (self.mock / "item.json").write_text(json.dumps(dict(number=1, html_url="https://github.com/owner/repo/issues/1", state="open")))

    def call(self, *args, body="A proposed comment", ok=True):
        return self.json_cli("comment-plus", "--expected-repo", "owner/repo", "--kind", "issue", "--number", "1", "--body", body, *args, ok=ok)

    def publish(self, plan):
        return ("--publish", "--request-id", plan["plan"]["request_id"], "--approve", plan["approval"])

    def write_calls(self):
        path = self.mock / "write-calls.jsonl"
        return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []

    def test_dry_run_requires_no_network_or_record(self):
        preview = self.call()
        self.assertEqual(preview["plan"]["body"], "A proposed comment")
        self.assertEqual(self.write_calls(), [])
        self.assertFalse((self.root / "data/owner/repo/writes").exists())
        self.call("--publish", ok=False)
        self.assertEqual(self.write_calls(), [])

    def test_approval_binds_comment_and_replay(self):
        preview = self.call()
        args = self.publish(preview)
        self.call(*args, body="Different text", ok=False)
        self.assertEqual(self.write_calls(), [])
        result = self.call(*args)
        self.assertEqual(result["comment"]["status"], "succeeded")
        self.assertEqual(result["state_change"]["status"], "not_requested")
        self.assertEqual(self.call(*args), result)
        self.assertEqual([call["method"] for call in self.write_calls()], ["GET", "POST"])
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())

    def test_comment_and_state_outcomes_are_separate(self):
        preview = self.call("--close")
        (self.mock / "fail-patch").touch()
        result = self.call(*self.publish(preview), "--close", ok=False)
        self.assertEqual(result["comment"]["status"], "succeeded")
        self.assertEqual(result["state_change"]["status"], "unknown")
        (self.mock / "fail-patch").unlink()
        self.call(*self.publish(preview), "--close", ok=False)
        self.assertEqual([call["method"] for call in self.write_calls()], ["GET", "POST", "PATCH"])
