"""Evaluation checkout retirement retains complete sealed installs offline."""

import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch


ROOT = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("agent_retire", ROOT / "tests-playbooks/seed/agent_retire.py")
agent_retire = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(agent_retire)


class RetirementTests(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.base = Path(temporary.name).resolve()
        self.run = self.base / "records/seed/runs/r01"
        self.output = self.run / "agents/a"
        self.output.mkdir(parents=True)
        self.root = self.base / "triage-eval-r01/a"
        self.checkout = self.root / "private"
        self.checkout.mkdir(parents=True)

        for args in (("init", "-q"), ("config", "user.name", "Test"), ("config", "user.email", "test@example.invalid")):
            self.git(*args)

        (self.checkout / "source.txt").write_text("Pinned source\n")
        self.git("add", "source.txt")
        self.git("commit", "-qm", "Initial")
        with (self.checkout / ".git/info/exclude").open("a") as out:
            out.write("\n/triage-o-mator/\n")

        self.install = self.checkout / "triage-o-mator"
        self.install.mkdir()
        (self.install / "ledger.jsonl").write_text('{"local":"retained"}\n')
        (self.install / "program").symlink_to(ROOT / "bin")
        record = {"lane": "private", "root": str(self.root), "private": {"path": str(self.checkout), "head": self.git("rev-parse", "HEAD")}, "launch": {"working_directory": str(self.install)}}
        (self.run / "agent-installs.json").write_text(json.dumps({"a": record}))
        self.seal = {"agent": "a", "assigned_working_directory": str(self.install), "task_complete_event": True, "final_output_found": True}
        for name, key in (("transcript.jsonl", "transcript_sha256"), ("commands.jsonl", "command_log_sha256"), ("final.md", "output_sha256")):
            path = self.output / name
            path.write_text("Sealed output\n")
            self.seal[key] = agent_retire.sha256(path)

        path = self.output / "artifact-digests.json"
        path.write_text(json.dumps([{"path": str(self.install / "ledger.jsonl"), "sha256": agent_retire.sha256(self.install / "ledger.jsonl")}]))
        self.seal["artifact_manifest_sha256"] = agent_retire.sha256(path)
        self.write_seal()

    def git(self, *args):
        return subprocess.check_output(["git", "-C", str(self.checkout), *args], text=True).strip()

    def write_seal(self):
        (self.output / "seal.json").write_text(json.dumps(self.seal))

    def retire(self):
        return agent_retire.retire(self.run, self.base, "a")

    def test_cli_retains_install_and_archivable_registered_path(self):
        before = agent_retire.signature(self.install)
        env = dict(os.environ, TRIAGE_EVAL_RECORDS=str(self.base / "records"), TRIAGE_EVAL_AGENT_ROOT=str(self.base))
        result = subprocess.run([str(ROOT / "tests-playbooks/run-eval.sh"), "agent-retire", "r01", "a"], env=env, text=True, capture_output=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        receipt = json.loads(result.stdout)
        self.assertEqual(receipt["status"], "complete")
        self.assertEqual(list(self.checkout.iterdir()), [self.install])
        self.assertEqual(agent_retire.signature(self.install), before)
        self.assertTrue((self.install / "program").is_symlink())
        self.assertEqual(self.retire(), receipt)

    def test_refuses_incomplete_corrupt_or_changed_state_before_deletion(self):
        self.seal["task_complete_event"] = False
        self.write_seal()
        with self.assertRaisesRegex(ValueError, "completed attempt"):
            self.retire()

        self.seal["task_complete_event"] = True
        self.write_seal()
        artifact = self.install / "ledger.jsonl"
        original = artifact.read_bytes()
        artifact.write_text("Changed")
        with self.assertRaisesRegex(ValueError, "artifact changed"):
            self.retire()

        artifact.write_bytes(original)
        (self.checkout / "source.txt").write_text("Changed")
        with self.assertRaisesRegex(ValueError, "HEAD changed or checkout"):
            self.retire()

        self.git("checkout", "--", "source.txt")
        (self.checkout / "unexpected.txt").write_text("Keep this")
        with self.assertRaisesRegex(ValueError, "HEAD changed or checkout"):
            self.retire()

        self.assertTrue((self.checkout / ".git").is_dir())
        self.assertFalse((self.output / "retirement.json").exists())

    def test_refuses_foreign_paths_and_sealed_record_changes(self):
        index = self.run / "agent-installs.json"
        original = index.read_bytes()
        data = json.loads(original)
        data["a"]["private"]["path"] = str(self.base)
        index.write_text(json.dumps(data))
        with self.assertRaisesRegex(ValueError, "Registered paths"):
            self.retire()

        index.write_bytes(original)
        (self.output / "final.md").write_text("Changed output")
        with self.assertRaisesRegex(ValueError, "Sealed record changed"):
            self.retire()

        self.assertTrue((self.checkout / "source.txt").exists())

    def test_interrupted_cleanup_resumes_without_git_or_install_loss(self):
        remove = agent_retire.shutil.rmtree

        def interrupted(path):
            remove(path)
            raise OSError("Injected interruption after Git removal")

        with patch.object(agent_retire.shutil, "rmtree", side_effect=interrupted), self.assertRaisesRegex(OSError, "Injected interruption"):
            self.retire()

        receipt = json.loads((self.output / "retirement.json").read_text())
        self.assertEqual(receipt["status"], "prepared")
        self.assertFalse((self.checkout / ".git").exists())
        self.assertTrue((self.install / "ledger.jsonl").is_file())
        self.assertEqual(self.retire()["status"], "complete")


if __name__ == "__main__":
    unittest.main()
