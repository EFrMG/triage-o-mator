"""Notification presentation changes leave retained evidence intact and use no live reads."""

import json
import subprocess

from support import CLAIM, ClosureStoreFixture


class NotificationStateTests(ClosureStoreFixture):
    def command(self, command, *args, ok=True):
        result = subprocess.run([str(self.root / "bin/cache"), command, *args], cwd=self.root, env=self.env,
                                capture_output=True, text=True, timeout=20)
        if not ok:
            self.assertNotEqual(result.returncode, 0, result.stdout)
            return result.stderr

        self.assertEqual(result.returncode, 0, result.stderr)
        return json.loads(result.stdout)

    def test_view_and_dismiss_are_local_and_bound_to_saved_records(self):
        watch = self.enroll()["watch"]
        history = self.save(watch["observations"][0], CLAIM)["history"]
        before = self.calls()

        self.command("notification-view", "--source", "watch", "--number", "1", "--checkpoint", watch["checksum"])
        self.command("notification-view", "--source", "action", "--number", "1", "--checkpoint", history["checksum"])
        self.assertEqual(self.command("notification-state")["rows"]["action:pr:1"]["viewed_checkpoint"], history["checksum"])
        self.command("notification-dismiss", "--source", "action", "--number", "1", "--checkpoint", history["checksum"])
        rows = self.command("notification-state")["rows"]
        self.assertEqual(rows["watch:pr:1"], dict(viewed_checkpoint=watch["checksum"], dismissed=False))
        self.assertEqual(rows["action:pr:1"], dict(viewed_checkpoint=history["checksum"], dismissed=True))
        self.assertEqual(self.calls(), before)
        self.assertTrue((self.root / "data/owner/repo/cache/watches/pr-1.json").exists())
        self.assertTrue((self.root / "data/owner/repo/external-closures/pr-1.json").exists())

        unavailable = self.root / "data/owner/repo/cache/watches/pr-2.json"
        unavailable.write_text("broken")
        catalog = self.command("attention-list")
        self.assertFalse(catalog["rows"][1]["selectable"])
        self.command("notification-dismiss", "--source", "watch", "--number", "2", "--checkpoint", catalog["checkpoint"])
        self.assertTrue(self.command("notification-state")["rows"]["watch:pr:2"]["dismissed"])

        unavailable_action = self.root / "data/owner/repo/external-closures/pr-2.json"
        unavailable_action.write_text("broken")
        action_catalog = self.command("action-list")
        self.assertFalse(action_catalog["rows"][1]["selectable"])
        self.command("notification-dismiss", "--source", "action", "--number", "2", "--checkpoint", action_catalog["checkpoint"])
        self.assertTrue(self.command("notification-state")["rows"]["action:pr:2"]["dismissed"])

        self.watch("watch-poll")
        stale = self.command("notification-view", "--source", "watch", "--number", "1", "--checkpoint", watch["checksum"], ok=False)
        self.assertIn("changed", stale)

        path = self.root / "data/owner/repo/local/notification-state.json"
        path.write_text(path.read_text().replace('"dismissed":true', '"dismissed":false'))
        self.assertIn("checksum mismatch", self.command("notification-state", ok=False))
