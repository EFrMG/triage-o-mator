"""Publishing is explicit, exact-plan bound, and never automatically retried."""

import json
import subprocess

from support import CheckoutTest


class CommentTests(CheckoutTest):
    def setUp(self):
        super().setUp()
        (self.mock / "gh").write_text('''#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
root = Path(os.environ["FAKE_GH_DIR"])
args = sys.argv[1:]
method = args[args.index("--method") + 1]
with (root / "calls").open("a") as out:
    out.write(json.dumps(dict(args=args, body=json.load(sys.stdin) if method == "POST" else None)) + "\\n")
if method == "GET":
    print((root / "item.json").read_text())
else:
    assert method == "POST" and args[-3].endswith("/comments")
    if (root / "fail").exists():
        sys.exit("connection lost")
    print(json.dumps(dict(id=42, html_url="https://github.com/owner/repo/issues/1#issuecomment-42")))
''')
        self.item = dict(number=1, html_url="https://github.com/owner/repo/issues/1")
        self.set_item()
        self.body = "A comment\n\nWith **Markdown** and 'quotes'."

    def set_item(self):
        (self.mock / "item.json").write_text(json.dumps(self.item))

    def call(self, *args, ok=True, kind="issue", body=None):
        result = subprocess.run([str(self.root / "bin/comment"), "--expected-repo", "owner/repo", "--kind", kind, "--number", "1", "--body", self.body if body is None else body, *args], env=self.env, cwd=self.root, text=True, capture_output=True)
        self.assertEqual(result.returncode == 0, ok, result.stdout + result.stderr)
        return json.loads(result.stdout) if result.stdout else None

    def publish_args(self, preview):
        return ("--publish", "--request-id", preview["plan"]["request_id"], "--approve", preview["approval"])

    def calls(self):
        path = self.mock / "calls"
        return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []

    def test_dry_run_is_offline_and_writes_nothing(self):
        before = sorted(str(p) for p in self.root.rglob("*"))
        preview = self.call()
        self.assertEqual(preview["plan"]["body"], self.body)
        self.assertEqual(self.calls(), [])
        self.assertEqual(before, sorted(str(p) for p in self.root.rglob("*")))
        self.call("--publish", ok=False)
        self.assertEqual(self.calls(), [])

    def test_exact_plan_required_and_successful_replay_does_not_post(self):
        preview = self.call()
        args = self.publish_args(preview)
        self.call(*args, body="changed text", ok=False)
        self.call(*args, kind="pr", ok=False)
        self.call(*args, "--host", "other.example", ok=False)
        self.assertEqual(self.calls(), [])
        result = self.call(*args)
        self.assertEqual(result["comment"]["status"], "succeeded")
        self.assertEqual(result["state_change"]["status"], "not_requested")
        self.assertEqual(self.call(*args), result)
        self.assertEqual(len(self.calls()), 2)
        self.assertEqual(self.calls()[1]["body"], dict(body=self.body))
        self.assertFalse((self.root / "data/owner/repo/ledger.jsonl").exists())

    def test_pr_posts_conversation_comment(self):
        self.item.update(html_url="https://github.com/owner/repo/pull/1", pull_request={})
        self.set_item()
        preview = self.call(kind="pr")
        self.call(*self.publish_args(preview), kind="pr")
        self.assertIn("repos/owner/repo/issues/1/comments", self.calls()[1]["args"])

    def test_wrong_live_target_never_posts(self):
        preview = self.call()
        self.item["html_url"] = "https://github.com/another/repo/issues/1"
        self.set_item()
        self.call(*self.publish_args(preview), ok=False)
        self.assertEqual(len(self.calls()), 1)

    def test_uncertain_post_is_recorded_and_never_retried(self):
        preview = self.call()
        (self.mock / "fail").touch()
        result = self.call(*self.publish_args(preview), ok=False)
        self.assertEqual(result["comment"]["status"], "unknown")
        (self.mock / "fail").unlink()
        self.call(*self.publish_args(preview), ok=False)
        self.assertEqual(len(self.calls()), 2)
        path = self.root / "data/owner/repo/writes" / (preview["plan"]["request_id"] + ".json")
        self.assertEqual(json.loads(path.read_text()), result)

    def test_changed_repo_and_empty_body_are_rejected(self):
        preview = self.call()
        self.call(body=" \n ", ok=False)
        (self.root / "config/repo").write_text("other/repo\n")
        self.call(*self.publish_args(preview), ok=False)
        self.assertEqual(self.calls(), [])

    def test_symlinked_install_keeps_write_record_inside_install(self):
        checkout = self.root / "checkout"
        checkout.mkdir()
        (self.root / "bin").rename(checkout / "bin")
        (self.root / "bin").symlink_to(checkout / "bin", target_is_directory=True)
        preview = self.call()
        self.call(*self.publish_args(preview))
        self.assertTrue((self.root / "data/owner/repo/writes").is_dir())
        self.assertFalse((checkout / "data").exists())
