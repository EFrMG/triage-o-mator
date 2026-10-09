#!/usr/bin/env python3
"""Small offline checks for the evaluation seed's safety and resume boundaries."""

import base64
from contextlib import redirect_stderr, redirect_stdout
import hashlib
import io
import json
from pathlib import Path
import subprocess
import sys
from tempfile import TemporaryDirectory
import unittest
from unittest.mock import patch

import seed
import agent_install
import test_handoff


class SeedTests(unittest.TestCase):
    def setUp(self):
        self.spec = json.loads((Path(__file__).parent / "fixtures.json").read_text())
        self.spec["run_id"] = "0"
        self.spec["marker"] += "-0"
        self.spec["description"] = f"triage-o-mator private evaluation fixture {self.spec['marker']}"

    def expanded_spec(self, root):
        """An expanded-case fixture over every base topic, editing one stand-in source file per topic."""
        issues = {}
        prs = {}

        for topic in self.spec["topics"]:
            issues[topic["id"]] = {"other_words_title": f"Other wording for {topic['id']}", "other_words_body": "Same trigger and failure, described differently.", "different_cause_title": f"Other wording for {topic['id']}", "different_cause_body": "Same visible symptom from another command.", "unresolved_title": f"Possibly related to {topic['id']}", "unresolved_body": "One discriminating detail is missing."}
            path = f"docs/{topic['id']}.md"
            (root / "docs").mkdir(exist_ok=True)
            (root / path).write_text(f"Base text for {topic['id']}.\n")
            prs[topic["id"]] = {"title": f"Clarify {topic['id']}", "body": "Please check this wording.", "path": path, "append": "\nAn added claim.\n"}

        fixture = {"issues": issues, "prs": prs, "comments": [{"item_id": f"{self.spec['topics'][0]['id']}-alt_duplicate", "body": "Follow-up detail."}]}

        return {**self.spec, "run_id": "r05", "marker": self.spec["marker"].removesuffix("-0") + "-r05", "expanded_cases": fixture}

    def test_new_run_requires_explicit_fixture_specification(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "fixtures.json").write_text(json.dumps(self.spec))
            arguments = ["seed.py", "--expected-repo", self.spec["repository"], "--run-id", "r05"]

            with patch.multiple(seed, HERE=root, RECORDS=root), patch.object(seed, "SPEC_PATH", root / "fixtures.json"), patch.object(sys, "argv", arguments), patch.object(seed, "verify_source") as verify, redirect_stderr(io.StringIO()) as errors:
                with self.assertRaises(SystemExit):
                    seed.main()

            self.assertIn("No fixture specification", errors.getvalue())
            verify.assert_not_called()

            (root / "r05.json").write_text(json.dumps({"issues": {}, "prs": {}}))

            with patch.multiple(seed, HERE=root, RECORDS=root), patch.object(seed, "SPEC_PATH", root / "fixtures.json"), patch.object(sys, "argv", arguments), patch.object(seed, "verify_source") as verify, redirect_stderr(io.StringIO()) as errors:
                with self.assertRaises(SystemExit):
                    seed.main()

            self.assertIn("explicit public_read_scope", errors.getvalue())
            verify.assert_not_called()

            fixture = {"issues": {}, "prs": {}, "public_read_scope": {"repository": "jesseduffield/lazygit", "corpus_id": "selected-corpus", "inventory_snapshot": "selected-inventory", "source_commit": self.spec["base_sha"], "mode": "fixed-snapshot offline"}, "briefing_batches": {"issue": [40, 40, 40], "pr": [30]}}
            (root / "r05.json").write_text(json.dumps(fixture))

            with patch.multiple(seed, HERE=root, RECORDS=root), patch.object(seed, "SPEC_PATH", root / "fixtures.json"), patch.object(sys, "argv", arguments), patch.object(seed, "verify_source", side_effect=RuntimeError("fixture loaded")) as verify:
                with self.assertRaisesRegex(RuntimeError, "fixture loaded"):
                    seed.main()

            self.assertEqual(fixture, verify.call_args.args[0]["expanded_cases"])

    def test_fixture_inventory_and_exact_reuse(self):
        entries = seed.planned(self.spec, [], [])
        self.assertEqual(90, len([entry for entry in entries if entry["kind"] == "issue"]))
        self.assertEqual(20, len([entry for entry in entries if entry["kind"] == "pr"]))
        self.assertTrue(all(entry["existing_number"] is None for entry in entries))

        first = entries[0]
        saved = {"number": 101, "title": first["title"], "body": first["body"]}
        self.assertEqual(101, seed.exact_match([saved], first["title"], first["body"])["number"])

        saved["title"] += " edited"

        with self.assertRaisesRegex(RuntimeError, "changed title or body"):
            seed.exact_match([saved], first["title"], first["body"])

        followups = seed.followup_comments(self.spec)
        self.assertEqual(10, len(followups))
        self.assertTrue(all("A maintainer answered" not in item["body"] for item in followups))

    def test_public_item_order_does_not_repeat_role_positions(self):
        issues, pulls = seed.expanded(self.spec)
        issue_positions = [tuple(item["id"].split("-", 1)[1] for item in issues[offset:offset + 9]) for offset in range(0, 90, 9)]
        pr_positions = [tuple(item["id"].split("-", 1)[1] for item in pulls[offset:offset + 2]) for offset in range(0, 20, 2)]

        self.assertEqual(10, len(set(issue_positions)))
        self.assertGreater(len(set(pr_positions)), 1)

    def test_expanded_cases_add_comparison_issues_and_edited_file_prs(self):
        with TemporaryDirectory() as directory, patch.object(seed, "SOURCE_CLONE", Path(directory)):
            spec = self.expanded_spec(Path(directory))
            issues, pulls = seed.expanded(spec)
            modified = [item for item in pulls if "base_content_sha256" in item]

            self.assertEqual((120, 30, 10), (len(issues), len(pulls), len(modified)))
            self.assertEqual(11, len(seed.followup_comments(spec)))
            self.assertEqual(150, len({item["public_id"] for item in issues + pulls}))

            for topic in spec["topics"]:
                related = {item["id"].removeprefix(topic["id"] + "-"): item for item in issues if item["id"].startswith(topic["id"] + "-")}
                self.assertEqual(related["alt_duplicate"]["title"], related["same_title_distinct"]["title"])
                self.assertNotEqual(related["clear_bug"]["title"], related["alt_duplicate"]["title"])
                self.assertNotEqual(related["clear_bug"]["body"], related["unresolved_pair"]["body"])

            for item in modified:
                base = (seed.SOURCE_CLONE / item["path"]).read_bytes()
                self.assertEqual(hashlib.sha256(base).hexdigest(), item["base_content_sha256"])
                self.assertNotEqual(base, item["content"].encode())

    def test_repo_guard_rejects_public_target_before_writes(self):
        with patch.object(seed, "api", side_effect=[{"login": self.spec["owner"]}, {"full_name": self.spec["repository"], "private": False, "permissions": {"push": True}}]) as mocked:
            with self.assertRaisesRegex(RuntimeError, "not this run's private fixture"):
                seed.verify_repo(self.spec)

        self.assertEqual(2, mocked.call_count)
        self.assertTrue(all(call.args[0] == "GET" for call in mocked.call_args_list))

    def test_derived_private_base_allows_only_dependabot_deletion(self):
        responses = [{"login": self.spec["owner"]}, {"full_name": self.spec["repository"], "private": True, "archived": False, "permissions": {"push": True}, "description": self.spec["description"], "default_branch": "master"}, {"enabled": False}, {"object": {"sha": "private-base"}}, {"ahead_by": 1, "files": [{"filename": ".github/dependabot.yml", "status": "removed"}]}]

        with patch.object(seed, "api", side_effect=responses):
            self.assertEqual("private-base", seed.verify_repo(self.spec)["_base_sha"])

        responses[-1] = {"ahead_by": 1, "files": [{"filename": "go.mod", "status": "modified"}]}

        with patch.object(seed, "api", side_effect=responses):
            with self.assertRaisesRegex(RuntimeError, "differs beyond"):
                seed.verify_repo(self.spec)

    def test_new_pr_branch_is_built_from_pinned_base(self):
        item = seed.planned(self.spec, [], [])[-1]
        calls = []

        def fake_api(method, path, body=None, missing_ok=False):
            calls.append((method, path, body))

            if method == "GET" and "/git/ref/heads/" in path:
                return None

            if method == "GET" and "/git/commits/" in path:
                return {"tree": {"sha": "base-tree"}}

            if path.endswith("/git/blobs"):
                return {"sha": "fixture-blob"}

            if path.endswith("/git/trees"):
                return {"sha": "fixture-tree"}

            if path.endswith("/git/commits"):
                return {"sha": "fixture-commit"}

            return {}

        with patch.object(seed, "api", side_effect=fake_api):
            self.assertEqual("fixture-commit", seed.ensure_branch(self.spec, item))

        self.assertEqual(["GET", "GET", "POST", "POST", "POST", "POST"], [call[0] for call in calls])
        self.assertEqual(self.spec["base_sha"], calls[4][2]["parents"][0])
        self.assertEqual(item["path"], calls[3][2]["tree"][0]["path"])
        self.assertEqual(f"refs/heads/{item['branch']}", calls[5][2]["ref"])

    def test_edited_pr_branch_binds_pinned_file_content(self):
        with TemporaryDirectory() as directory, patch.object(seed, "SOURCE_CLONE", Path(directory)):
            spec = self.expanded_spec(Path(directory))
            item = next(item for item in seed.planned(spec, [], []) if item["id"].endswith("-extra_pr"))
            calls = []

            def fake_api(method, path, body=None, missing_ok=False):
                calls.append((method, path, body))

                if method == "GET" and "/git/ref/heads/" in path:
                    return None

                if method == "GET" and "/git/commits/" in path:
                    return {"tree": {"sha": "base-tree"}}

                return {"sha": "created"}

            with patch.object(seed, "api", side_effect=fake_api):
                self.assertEqual("created", seed.ensure_branch(spec, item))

            self.assertEqual(item["content"], calls[2][2]["content"])
            self.assertEqual(item["path"], calls[3][2]["tree"][0]["path"])
            self.assertEqual([spec["base_sha"]], calls[4][2]["parents"])

            changed = dict(item, base_content_sha256="0" * 64)

            with patch.object(seed, "api", side_effect=fake_api):
                with self.assertRaisesRegex(RuntimeError, "Pinned base content changed"):
                    seed.ensure_branch(spec, changed)

    def test_existing_branch_must_keep_exact_file(self):
        item = seed.planned(self.spec, [], [])[-1]
        encoded = base64.b64encode(item["content"].encode()).decode()

        with patch.object(seed, "api", side_effect=[{"object": {"sha": "existing"}}, {"encoding": "base64", "content": encoded}]):
            self.assertEqual("existing", seed.ensure_branch(self.spec, item))

        with patch.object(seed, "api", side_effect=[{"object": {"sha": "existing"}}, {"encoding": "base64", "content": base64.b64encode(b"changed").decode()}]):
            with self.assertRaisesRegex(RuntimeError, "changed fixture content"):
                seed.ensure_branch(self.spec, item)

    def test_repository_creation_disables_actions_before_base_push(self):
        calls = []

        def fake_api(method, path, body=None, missing_ok=False):
            calls.append((method, path, body))

            if method == "GET" and "/git/ref/heads/" in path:
                return None

            if method == "GET" and path == f"repos/{self.spec['repository']}":
                return {"default_branch": "master"}

            if method == "GET" and "/contents/.github/dependabot.yml" in path:
                return None

            return {}

        with patch.object(seed, "api", side_effect=fake_api), patch.object(seed, "verify_repo", return_value={"full_name": self.spec["repository"], "_base_sha": self.spec["base_sha"]}), patch.object(seed.subprocess, "run") as pushed:
            pushed.return_value.returncode = 0
            self.assertEqual(self.spec["base_sha"], seed.ensure_repo_base(self.spec, None))

        self.assertEqual(["POST", "PUT", "GET", "GET", "GET"], [call[0] for call in calls])
        self.assertEqual("user/repos", calls[0][1])
        self.assertEqual({"enabled": False}, calls[1][2])
        self.assertIn(self.spec["base_sha"], pushed.call_args.args[0][-1])

    def test_repository_setup_removes_dependabot_config_before_fixtures(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            source = root / "source"
            config = source / ".github" / "dependabot.yml"
            config.parent.mkdir(parents=True)
            config.write_text("version: 2\nupdates: []\n")
            calls = []

            def fake_api(method, path, body=None, missing_ok=False):
                calls.append((method, path, body))

                if method == "GET" and "/git/ref/heads/" in path:
                    return None

                if method == "GET" and path == f"repos/{self.spec['repository']}":
                    return {"default_branch": "master"}

                if method == "GET" and "/contents/.github/dependabot.yml" in path:
                    return {"content": base64.b64encode(config.read_bytes()).decode(), "sha": "config-blob"}

                if method == "DELETE":
                    return {"commit": {"sha": "private-base"}}

                return {}

            with patch.object(seed, "SOURCE_CLONE", source), patch.multiple(seed, HERE=root, RECORDS=root), patch.object(seed, "api", side_effect=fake_api), patch.object(seed, "verify_repo", return_value={"_base_sha": "private-base"}), patch.object(seed.subprocess, "run") as pushed:
                pushed.return_value.returncode = 0
                self.assertEqual("private-base", seed.ensure_repo_base(self.spec, None))

            self.assertEqual("DELETE", calls[-1][0])
            self.assertEqual("config-blob", calls[-1][2]["sha"])
            setup = json.loads((root / "runs" / "0" / "dependabot-disable-state.json").read_text())
            self.assertEqual("private-base", setup["new_master"])

    def test_empty_settings_reply_and_empty_repository_ref(self):
        with patch.object(seed.time, "sleep"), patch.object(seed.subprocess, "run") as command:
            command.return_value.returncode = 0
            command.return_value.stdout = ""
            self.assertEqual({}, seed.api("PUT", "repos/example/test/actions/permissions", {"enabled": False}))

            command.return_value.returncode = 1
            command.return_value.stderr = "Git Repository is empty. (HTTP 409)"
            self.assertIsNone(seed.api("GET", "repos/example/test/git/ref/heads/master", missing_ok=True))

    def test_existing_install_is_verified_without_deletion(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            code = root / "code"
            clone = root / "clone"
            install = clone / "triage-o-mator"
            (code / "bin").mkdir(parents=True)
            (install / "config").mkdir(parents=True)
            (install / "data").mkdir()
            (install / ".triage-install.json").write_text(json.dumps({"repo": self.spec["repository"], "mode": "solo", "tool": str(code)}))
            (install / "config" / "repo").write_text(self.spec["repository"] + "\n")
            (install / "bin").symlink_to(code / "bin")
            (install / "data" / "old.json").write_text("old")

            with patch.object(seed, "CODE_ROOT", code), patch.object(seed, "clone_path", return_value=clone), patch.object(seed.subprocess, "run") as installer:
                seed.ensure_install(self.spec)

            self.assertTrue((install / "data" / "old.json").exists())
            self.assertTrue(code.exists())
            installer.assert_not_called()

    def test_existing_install_rejects_foreign_identity(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            install = root / "clone" / "triage-o-mator"
            install.mkdir(parents=True)
            (install / ".triage-install.json").write_text(json.dumps({"repo": "other/repo", "mode": "solo", "tool": str(root)}))

            with patch.object(seed, "CODE_ROOT", root), patch.object(seed, "clone_path", return_value=root / "clone"), patch.object(seed.subprocess, "run") as installer:
                with self.assertRaisesRegex(RuntimeError, "did not match"):
                    seed.ensure_install(self.spec)

            self.assertTrue(install.exists())
            installer.assert_not_called()

    def test_clone_replacement_requires_verified_previous_archive(self):
        with TemporaryDirectory() as directory:
            clone = Path(directory) / "lazygit-test"
            clone.mkdir()
            (clone / "old-marker").write_text("old")

            with patch.object(seed, "clone_path", return_value=clone), patch.object(seed, "verify_clone", side_effect=RuntimeError("old checkout")), patch.object(seed, "verify_old_clone_against_archive") as verified, patch.object(seed.subprocess, "run") as cloned:
                with self.assertRaisesRegex(RuntimeError, "requires a verified previous archive"):
                    seed.ensure_clone(self.spec, None)

            self.assertTrue((clone / "old-marker").exists())
            verified.assert_not_called()
            cloned.assert_not_called()

            def make_clone(*args, **kwargs):
                clone.mkdir()
                return None

            with patch.object(seed, "clone_path", return_value=clone), patch.object(seed, "verify_clone", side_effect=[RuntimeError("old checkout"), None]), patch.object(seed, "verify_old_clone_against_archive") as verified, patch.object(seed.subprocess, "run", side_effect=make_clone) as cloned:
                seed.ensure_clone(self.spec, "/verified/archive.tar.gz")

            verified.assert_called_once()
            cloned.assert_called_once()
            self.assertFalse((clone / "old-marker").exists())

    def test_same_run_preview_accepts_existing_clone_with_saved_state(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            clone = root / "lazygit-test"
            clone.mkdir()
            state = root / "runs" / "0" / "seed-state.json"
            state.parent.mkdir(parents=True)

            with patch.multiple(seed, HERE=root, RECORDS=root), patch.object(seed, "clone_path", return_value=clone), patch.object(seed, "verify_source"), patch.object(seed, "verify_repo", return_value=None), patch.object(sys, "argv", ["seed.py", "--expected-repo", self.spec["repository"], "--run-id", "0"]), patch.object(sys, "stderr", io.StringIO()), redirect_stdout(io.StringIO()):
                with self.assertRaises(SystemExit):
                    seed.main()

                state.write_text("{}")
                seed.main()

    def test_ledger_difference_exposes_lagging_pr_list(self):
        with TemporaryDirectory() as directory:
            clone = Path(directory)
            ledger = clone / "triage-o-mator" / "data" / self.spec["repository"] / "ledger.jsonl"
            ledger.parent.mkdir(parents=True)
            ledger.write_text(json.dumps({"kind": "issue", "number": 1}) + "\n")
            state = {"items": {"issue:one": 1, "pr:two": 2}}

            with patch.object(seed, "clone_path", return_value=clone):
                missing, unexpected = seed.ledger_difference(self.spec, state)

            self.assertEqual({("pr", 2)}, missing)
            self.assertEqual(set(), unexpected)

    def test_full_apply_records_110_keys_and_refuses_missing_saved_remote_items(self):
        spec = dict(self.spec)
        entries = seed.planned(spec, [], [])
        intent = [{key: value for key, value in entry.items() if key != "existing_number"} for entry in entries]
        followups = seed.followup_comments(spec)
        next_number = iter(range(200, 310))
        next_comment = iter(range(500, 510))

        def fake_api(method, path, body=None, missing_ok=False):
            if method == "GET" and path.endswith("/comments?per_page=100"):
                return []

            self.assertEqual("POST", method)
            self.assertTrue(path.endswith(("/issues", "/pulls", "/comments")))

            if path.endswith("/comments"):
                return {"id": next(next_comment)}

            return {"number": next(next_number)}

        with TemporaryDirectory() as directory:
            local_clone = Path(directory) / "lazygit-test"
            digest = hashlib.sha256(json.dumps({"spec": spec, "operations": intent, "followup_comments": followups, "create_private_repo": spec["repository"], "disable_dependabot_config": ".github/dependabot.yml", "replace_clone": str(local_clone), "previous_archive": None, "previous_archive_sha256": None, "install_path": str(local_clone / "triage-o-mator")}, sort_keys=True, separators=(",", ":")).encode()).hexdigest()

            with patch.multiple(seed, HERE=Path(directory), RECORDS=Path(directory)), patch.object(seed, "clone_path", return_value=local_clone), patch.object(seed, "verify_source"), patch.object(seed, "verify_repo", return_value=None), patch.object(seed, "ensure_repo_base", return_value=spec["base_sha"]), patch.object(seed, "ensure_clone"), patch.object(seed, "ensure_install") as install, patch.object(seed, "ledger_difference", return_value=(set(), set())), patch.object(seed, "ensure_branch"), patch.object(seed, "existing_branch_content"), patch.object(seed, "pages", return_value=[{"state": "open", "number": n} for n in range(290, 310)]), patch.object(seed, "api", side_effect=fake_api) as api, patch.object(seed.subprocess, "run") as local_commands, patch.object(sys, "argv", ["seed.py", "--expected-repo", spec["repository"], "--run-id", "0", "--apply", "--plan-sha256", digest]), redirect_stdout(io.StringIO()):
                seed.main()
                state = json.loads((Path(directory) / "runs" / "0" / "seed-state.json").read_text())
                self.assertEqual(110, len(state["items"]))
                self.assertEqual(10, len(state["comments"]))
                self.assertTrue(state["install_ready"])
                install.assert_called_once_with(spec)
                self.assertEqual(2, local_commands.call_count)
                self.assertEqual(130, api.call_count)

                with self.assertRaisesRegex(RuntimeError, "absent or has a different remote number"):
                    seed.main()

                self.assertEqual(130, api.call_count)

    def test_agent_handoff_freezes_a_verified_exact_scope(self):
        with TemporaryDirectory() as directory:
            run_dir = Path(directory) / "runs" / "0"
            run_dir.mkdir(parents=True)
            operations = [{"kind": "issue", "id": f"issue-{n}"} for n in range(1, 91)] + [{"kind": "pr", "id": f"pr-{n}"} for n in range(91, 111)]
            items = {f"{operation['kind']}:{operation['id']}": index for index, operation in enumerate(operations, 1)}
            preview = {"repository": self.spec["repository"], "plan_sha256": "test-plan", "previous_archive_sha256": "old-archive", "push_base_sha": self.spec["base_sha"], "operations": operations}
            state = {"repository": self.spec["repository"], "plan_sha256": "test-plan", "install_ready": True, "items": items}
            audit = {"repository": self.spec["repository"], "plan_sha256": "test-plan", "remote_issues": 90, "remote_prs": 20, "ledger_rows": 110, "closed_bot_prs_excluded": 0, "action_policy": "stage"}

            for name, value in (("preview", preview), ("seed-state", state), ("audit", audit)):
                (run_dir / f"{name}.json").write_text(json.dumps(value))

            (run_dir / "gold.json").write_text("{}\n")

            with patch.multiple(seed, HERE=Path(directory), RECORDS=Path(directory)), patch.object(sys, "argv", ["test_handoff.py", "--run-id", "0"]), redirect_stdout(io.StringIO()):
                test_handoff.main()

            handoff = json.loads((run_dir / "test-handoff.json").read_text())
            self.assertEqual(110, len(handoff["seeded_keys"]))
            self.assertEqual(hashlib.sha256(b"{}\n").hexdigest(), handoff["gold_sha256"])
            self.assertIn("auto-triage.md", handoff["playbook_sha256"])
            agent_text = (run_dir / "agent-handoff.md").read_text()
            self.assertIn("does not assert that any playbook pass has run", agent_text)
            self.assertIn("It is not a task", agent_text)

            for withheld in ("](", "gold", "plan.md", "seed/", "batch", handoff["gold_sha256"], "test-plan"):
                self.assertNotIn(withheld, agent_text)

    def test_new_run_handoff_needs_scope_and_run_specific_gold(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            run_dir = root / "runs" / "r05"
            run_dir.mkdir(parents=True)
            operations = [{"kind": "issue", "id": f"issue-{n}"} for n in range(1, 121)] + [{"kind": "pr", "id": f"pr-{n}"} for n in range(121, 151)]
            items = {f"{operation['kind']}:{operation['id']}": index for index, operation in enumerate(operations, 1)}
            preview = {"repository": self.spec["repository"], "plan_sha256": "test-plan", "previous_archive_sha256": "old-archive", "push_base_sha": self.spec["base_sha"], "operations": operations, "public_read_scope": {"repository": "jesseduffield/lazygit", "corpus_id": "corpus", "inventory_snapshot": "inventory", "source_commit": self.spec["base_sha"], "mode": "fixed-snapshot offline"}, "briefing_batches": {"issue": [40, 40, 40], "pr": [30]}}
            state = {"repository": self.spec["repository"], "plan_sha256": "test-plan", "install_ready": True, "items": items}
            audit = {"repository": self.spec["repository"], "plan_sha256": "test-plan", "remote_issues": 120, "remote_prs": 30, "ledger_rows": 150, "closed_bot_prs_excluded": 0, "action_policy": "stage"}

            for name, value in (("preview", preview), ("seed-state", state), ("audit", audit)):
                (run_dir / f"{name}.json").write_text(json.dumps(value))

            (run_dir / "gold.json").write_text("{\"run\": \"r05\"}\n")

            with patch.multiple(seed, HERE=root, RECORDS=root), patch.object(sys, "argv", ["test_handoff.py", "--run-id", "r05"]), redirect_stdout(io.StringIO()):
                test_handoff.main()

            handoff = json.loads((run_dir / "test-handoff.json").read_text())
            self.assertEqual({"issue": [40, 40, 40], "pr": [30]}, handoff["briefing_batches"])
            self.assertEqual({"issue": 120, "pr": 30}, handoff["counts"])
            self.assertEqual(hashlib.sha256((run_dir / "gold.json").read_bytes()).hexdigest(), handoff["gold_sha256"])
            self.assertIn("120 open issues and 30 open PRs", (run_dir / "agent-handoff.md").read_text())
            self.assertFalse((run_dir / "test-handoff.md").exists())

            (run_dir / "gold.json").unlink()

            with patch.multiple(seed, HERE=root, RECORDS=root), patch.object(sys, "argv", ["test_handoff.py", "--run-id", "r05"]), self.assertRaisesRegex(RuntimeError, "answer guide is missing"):
                test_handoff.main()

            (run_dir / "gold.json").write_text("{}\n")
            preview.pop("public_read_scope")
            (run_dir / "preview.json").write_text(json.dumps(preview))

            with patch.multiple(seed, HERE=root, RECORDS=root), patch.object(sys, "argv", ["test_handoff.py", "--run-id", "r05"]), self.assertRaisesRegex(RuntimeError, "explicit public scope"):
                test_handoff.main()

    def test_agent_install_is_isolated_and_drops_earlier_public_decisions(self):
        with TemporaryDirectory() as directory:
            root = Path(directory)
            kit = root / "kit" / "seed"
            run_dir = kit / "runs" / "r05"
            run_dir.mkdir(parents=True)
            scope = {"repository": "jesseduffield/lazygit", "corpus_id": "corpus", "inventory_snapshot": "inventory", "source_commit": "", "mode": "fixed-snapshot offline"}
            heads = {}

            for name, repository in (("private", self.spec["repository"]), ("public", scope["repository"])):
                checkout = root / name
                install = checkout / "triage-o-mator"
                data = install / "data" / repository
                (data / "raw").mkdir(parents=True)
                (install / "config").mkdir()
                (install / "config" / "repo").write_text(repository + "\n")
                (install / "bin").symlink_to(seed.CODE_ROOT / "bin")
                (data / "raw" / "fetch_meta.json").write_text("{}\n")
                (checkout / "base.txt").write_text("pinned\n")
                subprocess.run(["git", "init", "--quiet", str(checkout)], check=True)
                (checkout / ".git" / "info" / "exclude").write_text("/triage-o-mator/\n")
                subprocess.run(["git", "-C", str(checkout), "add", "base.txt"], check=True)
                subprocess.run(["git", "-C", str(checkout), "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "--quiet", "-m", "base"], check=True)
                heads[name] = agent_install.git(checkout, "rev-parse", "HEAD")

            public_data = root / "public" / "triage-o-mator" / "data" / scope["repository"]
            (public_data / "cache" / "corpora").mkdir(parents=True)
            (public_data / "cache" / "corpora" / "corpus.plan.json").write_text("{}\n")
            (public_data / "pr-assessments").mkdir()
            (public_data / "pr-assessments" / "earlier.json").write_text("{}\n")
            (root / "public" / "triage-o-mator" / "reports").mkdir()
            rows = [{"number": 1, "kind": "issue", "state": "open", "title": "Kept", "action": "close", "triaged_by": "agent:earlier", "reason": "an earlier call", "item_score": {"total": 9}, "proposed_labels": ["bug"]}, {"number": 2, "kind": "pr", "state": "open", "title": "Untouched", "action": "", "triaged_by": "", "reason": "", "proposed_labels": []}]
            (public_data / "ledger.jsonl").write_text("".join(json.dumps(row) + "\n" for row in rows))
            (root / "private" / "triage-o-mator" / "data" / self.spec["repository"] / "ledger.jsonl").write_text("")

            playbooks = {path.name: test_handoff.sha256(path) for path in sorted((seed.CODE_ROOT / "prompts").glob("*.md"))}
            handoff = {"repository": self.spec["repository"], "source_commit": heads["public"], "playbook_sha256": playbooks, "public_read_scope": scope, "install_sha256": test_handoff.install_signature(root / "private" / "triage-o-mator")}
            (run_dir / "test-handoff.json").write_text(json.dumps(handoff))
            (run_dir / "audit.json").write_text(json.dumps({"local_checkout": heads["private"]}))
            (run_dir / "preview.json").write_text(json.dumps({"clone_path": str(root / "private"), "install_path": str(root / "private" / "triage-o-mator")}))
            (run_dir / "agent-handoff.md").write_text("starting state\n")
            patches = (patch.multiple(seed, HERE=kit, RECORDS=kit), patch.object(seed, "SOURCE_CLONE", root / "public"), patch.object(agent_install, "AGENT_ROOT", root / "agents"), patch.object(sys, "argv", ["agent_install.py", "--run-id", "r05", "--agent", "a"]))

            with patches[0], patches[1], patches[2], patches[3], redirect_stdout(io.StringIO()):
                agent_install.main()

                with self.assertRaisesRegex(RuntimeError, "already exists"):
                    agent_install.main()

                (root / "private" / "triage-o-mator" / "config" / "repo").write_text("someone/else\n")

                with patch.object(sys, "argv", ["agent_install.py", "--run-id", "r05", "--agent", "b"]), self.assertRaisesRegex(RuntimeError, "changed since test-start"):
                    agent_install.main()

            agent = root / "agents" / "triage-eval-r05" / "a"
            ledger = [json.loads(line) for line in (agent / "public" / "triage-o-mator" / "data" / scope["repository"] / "ledger.jsonl").read_text().splitlines()]
            self.assertEqual([("Kept", "", "", []), ("Untouched", "", "", [])], [(row["title"], row["action"], row["triaged_by"], row["proposed_labels"]) for row in ledger])
            self.assertNotIn("item_score", ledger[0])
            self.assertFalse((agent / "public" / "triage-o-mator" / "data" / scope["repository"] / "pr-assessments").exists())
            self.assertFalse((agent / "public" / "triage-o-mator" / "reports").exists())
            self.assertTrue((agent / "public" / "triage-o-mator" / "data" / scope["repository"] / "cache" / "corpora" / "corpus.plan.json").is_file())
            self.assertEqual("pinned\n", (agent / "private" / "base.txt").read_text())
            self.assertEqual("", agent_install.git(agent / "private", "status", "--porcelain"))
            self.assertEqual("no-push://public-read-only", agent_install.git(agent / "public", "remote", "get-url", "--push", "origin"))
            self.assertEqual(["agent-handoff.md", "private", "public"], sorted(path.name for path in agent.iterdir()))
            record = json.loads((run_dir / "agent-installs.json").read_text())
            self.assertEqual(["a"], list(record))
            self.assertEqual((2, 1), (record["a"]["public"]["ledger_rows"], record["a"]["public"]["ledger_rows_cleared"]))


if __name__ == "__main__":
    unittest.main()
