"""Evaluation fixture creation and checkout retirement preserve reviewed boundaries.""""

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


class MultiFileSeedTests(unittest.TestCase):
    def setUp(self):
        import sys

        self.addCleanup(setattr, sys, "path", sys.path[:])
        sys.path.insert(0, str(ROOT / "tests-playbooks/seed"))
        import seed
        import audit

        self.seed = seed
        self.audit = audit
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.source = Path(temporary.name)
        self.source.joinpath("caller.go").write_text("old call\n")
        self.source.joinpath("helper.go").write_text("old helper\n")
        self.spec = {"repository": "test/private", "base_sha": "base", "marker": "r06"}
        self.edits = {"files": [{"path": "caller.go", "replace": {"old": "old call", "new": "new call"}}, {"path": "helper.go", "append": "new helper\n"}]}
        self.root_patch = patch.object(seed, "SOURCE_CLONE", self.source)
        self.root_patch.start()
        self.addCleanup(self.root_patch.stop)
        self.files = seed.expanded_pr_files(self.edits)
        self.item = {"files": self.files, "branch": "fixture", "public_id": "p01"}

    def test_multifile_commit_and_exact_audit(self):
        spec = json.loads((ROOT / "tests-playbooks/seed/fixtures.json").read_text())
        spec["topics"] = spec["topics"][:1]
        topic_id = spec["topics"][0]["id"]
        issue = {f"{prefix}_{field}": "Fixture text" for prefix in ("other_words", "different_cause", "unresolved") for field in ("title", "body")}
        spec["expanded_cases"] = {"issues": {topic_id: issue}, "prs": {topic_id: {"title": "Update behavior", "body": "Review the changes", **self.edits}}}
        planned = next(item for item in self.seed.planned(spec, [], []) if item["id"].endswith("-extra_pr"))
        self.assertEqual(self.files, planned["files"])
        self.assertNotIn("path", planned)
        expanded_spec = json.loads(json.dumps(spec))
        expanded_spec["expanded_cases"].update(extra_issues=[{"id": "extra-question", "title": "Question", "body": "What was the input?"}], extra_prs=[{"id": "extra-change", "title": "Change", "body": "Please review", **self.edits}], comments=[{"kind": "pr", "item_id": "extra-change", "body": "Please withdraw this proposal."}])
        expanded = self.seed.planned(expanded_spec, [], [])
        self.assertEqual(len(self.seed.planned(spec, [], [])) + 2, len(expanded))
        all_items = sum(self.seed.expanded(expanded_spec), [])
        self.assertEqual(len(all_items), len({item["public_id"] for item in all_items}))
        followups = self.seed.followup_comments(expanded_spec)
        self.assertEqual("pr:extra-change", self.seed.followup_key(followups[-1]))
        expanded_spec["expanded_cases"]["extra_issues"].append(expanded_spec["expanded_cases"]["extra_issues"][0])

        with self.assertRaisesRegex(RuntimeError, "duplicate supplemental"):
            self.seed.planned(expanded_spec, [], [])

        changed_spec = json.loads(json.dumps(spec))
        changed_spec["expanded_cases"]["prs"][topic_id]["files"][1]["append"] = "different change\n"
        self.assertNotEqual(planned, next(item for item in self.seed.planned(changed_spec, [], []) if item["id"].endswith("-extra_pr")))
        calls = []

        def api(method, path, body=None, missing_ok=False):
            calls.append((method, path, body))

            if "/git/ref/" in path:
                return None

            if method == "GET" and "/git/commits/" in path:
                return {"tree": {"sha": "base-tree"}}

            return {"sha": str(len(calls))}

        with patch.object(self.seed, "api", side_effect=api):
            self.seed.ensure_branch(self.spec, self.item)

        tree = next(body for _, path, body in calls if path.endswith("/git/trees"))
        self.assertEqual(["caller.go", "helper.go"], [entry["path"] for entry in tree["tree"]])
        commits = [body for _, path, body in calls if path.endswith("/git/commits") and body]
        self.assertEqual(1, len(commits))
        self.assertEqual(["base"], commits[0]["parents"])
        actual = [{"filename": file["path"], "status": "modified"} for file in self.files]

        def contents(method, path, *args, **kwargs):
            import base64

            file = next(file for file in self.files if f"/{file['path']}?" in path)
            return {"encoding": "base64", "content": base64.b64encode(file["content"].encode()).decode()}

        with patch.object(self.seed, "api", side_effect=contents):
            self.audit.audit_pr_files("test/private", self.item, actual, "pr:1")

            for wrong in (actual[:1], actual + [{"filename": "extra.go", "status": "added"}]):
                with self.assertRaisesRegex(RuntimeError, "exact expected file set"):
                    self.audit.audit_pr_files("test/private", self.item, wrong, "pr:1")

        with patch.object(self.seed, "api", return_value={"encoding": "base64", "content": "d3Jvbmc="}):
            with self.assertRaisesRegex(RuntimeError, "differs from its preview"):
                self.audit.audit_pr_files("test/private", self.item, actual, "pr:1")

        self.source.joinpath("helper.go").write_text("changed base\n")

        with patch.object(self.seed, "api") as api:
            with self.assertRaisesRegex(RuntimeError, "Pinned base content changed"):
                self.seed.ensure_branch(self.spec, self.item)

            api.assert_not_called()

    def test_edit_validation_and_resume_checks_every_file(self):
        import base64

        for edits in ([self.edits["files"][0]] * 2, [{"path": "../outside", "append": "bad"}], [{"path": "caller.go", "append": ""}]):
            with self.assertRaises(RuntimeError):
                self.seed.expanded_pr_files({"files": edits})

        replies = [{"object": {"sha": "existing"}}, {"encoding": "base64", "content": base64.b64encode(self.files[0]["content"].encode()).decode()}, {"encoding": "base64", "content": "d3Jvbmc="}]

        with patch.object(self.seed, "api", side_effect=replies) as api:
            with self.assertRaisesRegex(RuntimeError, "changed fixture content"):
                self.seed.ensure_branch(self.spec, self.item)

            self.assertTrue(all(call.args[0] == "GET" for call in api.call_args_list))


if __name__ == "__main__":
    unittest.main()
