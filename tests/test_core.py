"""The install, ledger, and duplicate workflows that a maintainer uses first."""

import csv
import fcntl
import hashlib
import json
import os
import select
import shutil
import subprocess
import tempfile
import unittest
from datetime import datetime, timedelta, timezone
from pathlib import Path

from support import FAKE_GH, ROOT, Workspace, item, repository, summary


class InstallTests(unittest.TestCase):
    def test_solo_install_in_linked_worktree(self):
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            repository = base / "repository"
            worktree = base / "worktree"
            subprocess.run(["git", "init", "-q", str(repository)], check=True)
            subprocess.run(["git", "-C", str(repository), "config", "user.name", "Test"], check=True)
            subprocess.run(["git", "-C", str(repository), "config", "user.email", "test@example.com"], check=True)
            (repository / "README.md").write_text("fixture\n")
            subprocess.run(["git", "-C", str(repository), "add", "README.md"], check=True)
            subprocess.run(["git", "-C", str(repository), "-c", "commit.gpgsign=false", "commit", "-qm", "Initial"], check=True)
            subprocess.run(["git", "-C", str(repository), "worktree", "add", "-q", "--detach", str(worktree)], check=True)

            env = dict(os.environ, XDG_CONFIG_HOME=str(base / "config-home"))
            command = [str(ROOT / "bin/install-to"), str(worktree), "--repo", "owner/repo", "--offline"]
            exclude = Path(subprocess.run(["git", "-C", str(worktree), "rev-parse", "--git-path", "info/exclude"], check=True, capture_output=True, text=True).stdout.strip())
            if not exclude.is_absolute():
                exclude = worktree / exclude

            before = exclude.read_bytes()

            preview = subprocess.run([*command, "--solo", "--dry-run"], env=env, capture_output=True, text=True)
            self.assertEqual(preview.returncode, 0, preview.stderr)
            self.assertEqual(exclude.read_bytes(), before)
            self.assertFalse((worktree / "triage-o-mator").exists())
            self.assertFalse((base / "config-home").exists())

            for _ in range(2):
                result = subprocess.run([*command, "--solo"], env=env, capture_output=True, text=True)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(exclude.read_text().splitlines().count("/triage-o-mator/"), 1)
                status = subprocess.run(["git", "-C", str(worktree), "status", "--porcelain"], check=True, capture_output=True, text=True)
                self.assertEqual(status.stdout, "")

            preview = subprocess.run([*command, "--adopt", "--dry-run"], env=env, capture_output=True, text=True)
            self.assertEqual(preview.returncode, 0, preview.stderr)
            self.assertEqual(exclude.read_text().splitlines().count("/triage-o-mator/"), 1)
            staged = subprocess.run(["git", "-C", str(worktree), "diff", "--cached", "--name-only"], check=True, capture_output=True, text=True)
            self.assertEqual(staged.stdout, "")

            result = subprocess.run([*command, "--adopt"], env=env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertNotIn("/triage-o-mator/", exclude.read_text().splitlines())
            staged = subprocess.run(["git", "-C", str(worktree), "diff", "--cached", "--name-only"], check=True, capture_output=True, text=True)
            self.assertIn("triage-o-mator/config/repo", staged.stdout.splitlines())

    def test_install_dry_run_and_work_root(self):
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            checkout = base / "checkout"
            target = base / "target"
            target.mkdir()
            shutil.copytree(ROOT / "bin", checkout / "bin", ignore=shutil.ignore_patterns("triage-o-mator", "__pycache__"))
            for name in ("config", "themes", "prompts", "docs"):
                shutil.copytree(ROOT / name, checkout / name)
            for name in ("AGENTS.md", "README.md"):
                shutil.copyfile(ROOT / name, checkout / name)

            subprocess.run(["git", "init", "-q", str(target)], check=True)
            subprocess.run(["git", "-C", str(target), "remote", "add", "origin", "https://github.com/owner/repo.git"], check=True)
            env = dict(os.environ, XDG_CONFIG_HOME=str(base / "config-home"))
            command = [str(checkout / "bin/install-to"), str(target), "--offline"]
            preview = subprocess.run([*command, "--dry-run"], env=env, capture_output=True, text=True)
            self.assertEqual(preview.returncode, 0, preview.stderr)
            self.assertFalse((target / "triage-o-mator").exists())
            self.assertFalse((target / "AGENTS.md").exists())

            result = subprocess.run(command, env=env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("/data/*/*/reposition/", (target / "triage-o-mator/.gitignore").read_text())
            install = target / "triage-o-mator"
            self.assertTrue((install / "bin").is_symlink())
            self.assertTrue((install / "AGENTS.md").is_symlink())
            self.assertIn("<!-- triage-o-mator:begin -->", (target / "AGENTS.md").read_text())
            self.assertEqual((install / "config/repo").read_text().strip(), "owner/repo")
            self.assertEqual(json.loads((install / "config/taxonomy.json").read_text())["label_catalog"]["status"], "pending")
            self.assertFalse((install / "config/taxonomy.md").exists())
            self.assertFalse((checkout / "data").exists())

            script = subprocess.run([str(install / "bin/group"), "create", "--title", "Review", "--description", "Related reports", "--by", "operator"], cwd=install, env=env, capture_output=True, text=True)
            self.assertEqual(script.returncode, 0, script.stderr)
            self.assertTrue(list((install / "data/owner/repo/groups").glob("*.json")))
            self.assertFalse((checkout / "data").exists())

            (target / "AGENTS.md").write_text("# Own instructions\n")
            result = subprocess.run(command, env=env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertEqual((target / "AGENTS.md").read_text(), "# Own instructions\n")
            result = subprocess.run([*command, "--yes"], env=env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("<!-- triage-o-mator:begin -->", (target / "AGENTS.md").read_text())

            claude_target = base / "claude-target"
            claude_target.mkdir()
            subprocess.run(["git", "init", "-q", str(claude_target)], check=True)
            subprocess.run(["git", "-C", str(claude_target), "remote", "add", "origin", "https://github.com/owner/repo.git"], check=True)
            (claude_target / "CLAUDE.md").write_text("# Existing Claude instructions\n")
            result = subprocess.run([str(checkout / "bin/install-to"), str(claude_target), "--offline"], env=env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            self.assertIn("<!-- triage-o-mator:begin -->", (claude_target / "AGENTS.md").read_text())
            self.assertEqual((claude_target / "CLAUDE.md").read_text(), "# Existing Claude instructions\n")

    def test_github_label_catalog_install_and_reconcile(self):
        with tempfile.TemporaryDirectory() as directory:
            base = Path(directory)
            target = base / "target"
            mock = base / "mock"
            target.mkdir()
            mock.mkdir()
            subprocess.run(["git", "init", "-q", str(target)], check=True)
            (mock / "gh").write_text(FAKE_GH)
            (mock / "gh").chmod(0o755)
            endpoint = "repos/owner/repo/labels?per_page=100"
            responses = {endpoint: {"data": [{"id": 1, "name": "bug", "color": "FF0000", "description": "A defect"}, {"id": 2, "name": "old", "color": "00ff00", "description": None}]}}
            (mock / "responses.json").write_text(json.dumps(responses))
            env = dict(os.environ, PATH=str(mock) + os.pathsep + os.environ["PATH"], FAKE_GH_DIR=str(mock), XDG_CONFIG_HOME=str(base / "config-home"))
            command = [str(ROOT / "bin/install-to"), str(target), "--repo", "owner/repo", "--solo"]

            preview = subprocess.run([*command, "--dry-run"], env=env, capture_output=True, text=True)
            self.assertEqual(preview.returncode, 0, preview.stderr)
            self.assertFalse((target / "triage-o-mator").exists())

            installed = subprocess.run(command, env=env, capture_output=True, text=True)
            self.assertEqual(installed.returncode, 0, installed.stderr)
            taxonomy_path = target / "triage-o-mator/config/taxonomy.json"
            taxonomy = json.loads(taxonomy_path.read_text())
            catalog = taxonomy["label_catalog"]
            self.assertEqual(catalog["repository"], "owner/repo")
            self.assertEqual(catalog["status"], "observed")
            self.assertEqual([row["name"] for row in catalog["labels"]], ["bug", "old"])
            self.assertTrue(catalog["observed_at"])

            calls_before_show = (mock / "calls.jsonl").read_bytes()
            shown = subprocess.run([str(target / "triage-o-mator/bin/label-catalog"), "show"], env=env, capture_output=True, text=True)
            self.assertEqual(shown.returncode, 0, shown.stderr)
            self.assertEqual(json.loads(shown.stdout), catalog)
            self.assertEqual((mock / "calls.jsonl").read_bytes(), calls_before_show)

            settings = [str(target / "triage-o-mator/bin/taxonomy-settings"), "set-guidance", "--expected-repo", "owner/repo"]
            guidance = subprocess.run([*settings, "--label-id", "2", "--expected-name", "old", "--expected", "", "--value", "Keep this history"], env=env, capture_output=True, text=True)
            self.assertEqual(guidance.returncode, 0, guidance.stderr)
            action_guidance = subprocess.run([*settings, "--action", "none", "--expected", "No conversation or state change is justified now. Proposed labels remain independent.", "--value", "No conversation needed"], env=env, capture_output=True, text=True)
            self.assertEqual(action_guidance.returncode, 0, action_guidance.stderr)
            stale = subprocess.run([*settings, "--label-id", "2", "--expected-name", "old", "--expected", "", "--value", "Overwrite"], env=env, capture_output=True, text=True)
            self.assertNotEqual(stale.returncode, 0)
            self.assertEqual(json.loads(taxonomy_path.read_text())["label_catalog"]["labels"][1]["guidance"], "Keep this history")
            responses[endpoint]["data"] = [{"id": 2, "name": "new", "color": "112233", "description": "Renamed"}, {"id": 3, "name": "added", "color": "abcdef", "description": "New"}]
            (mock / "responses.json").write_text(json.dumps(responses))
            sync = [str(target / "triage-o-mator/bin/label-catalog"), "sync", "--expected-repo", "owner/repo"]
            before = taxonomy_path.read_bytes()
            dry_run = subprocess.run([*sync, "--dry-run"], env=env, capture_output=True, text=True)
            self.assertEqual(dry_run.returncode, 0, dry_run.stderr)
            self.assertEqual(taxonomy_path.read_bytes(), before)

            result = subprocess.run(sync, env=env, capture_output=True, text=True)
            self.assertEqual(result.returncode, 0, result.stderr)
            catalog = json.loads(taxonomy_path.read_text())["label_catalog"]
            self.assertEqual([(row["id"], row["name"]) for row in catalog["labels"]], [(3, "added"), (2, "new")])
            self.assertEqual(catalog["labels"][1]["guidance"], "Keep this history")
            self.assertEqual(catalog["labels"][1]["previous_names"], ["old"])
            self.assertEqual([row["name"] for row in catalog["retired"]], ["bug"])
            self.assertTrue(catalog["retired"][0]["retired_at"])
            self.assertEqual(json.loads(taxonomy_path.read_text())["action_guidance"]["none"], "No conversation needed")

            responses[endpoint] = {"data": [{"name": "broken"}]}
            (mock / "responses.json").write_text(json.dumps(responses))
            before = taxonomy_path.read_bytes()
            failed = subprocess.run(sync, env=env, capture_output=True, text=True)
            self.assertNotEqual(failed.returncode, 0)
            self.assertEqual(taxonomy_path.read_bytes(), before)
            self.assertTrue(all("POST" not in call and "PATCH" not in call and "DELETE" not in call for call in map(json.loads, (mock / "calls.jsonl").read_text().splitlines())))

            switched = subprocess.run([*command, "--repo", "other/repo", "--offline"], env=env, capture_output=True, text=True)
            self.assertEqual(switched.returncode, 0, switched.stderr)
            self.assertEqual(json.loads(taxonomy_path.read_text())["label_catalog"]["status"], "pending")
            restored = subprocess.run([*command, "--offline"], env=env, capture_output=True, text=True)
            self.assertEqual(restored.returncode, 0, restored.stderr)
            self.assertEqual(json.loads(taxonomy_path.read_text())["label_catalog"]["labels"][1]["guidance"], "Keep this history")


class LabelDefinitionTests(Workspace):
    def test_exact_github_preview_and_local_action_changes(self):
        (self.mock / "labels.json").write_text(json.dumps([dict(id=7, name="bug", description="Old", color="ff0000")]))
        (self.mock / "gh").write_text('''#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
root = Path(os.environ["FAKE_GH_DIR"])
args = sys.argv[1:]
with (root / "calls.jsonl").open("a") as out:
    out.write(json.dumps(args) + "\\n")
labels = json.loads((root / "labels.json").read_text())
method = args[args.index("--method") + 1]
if method == "GET" and "--paginate" in args:
    for label in labels:
        print(json.dumps(label))
    sys.exit(0)
if method not in ("POST", "PATCH", "DELETE") or args[0] != "api" or (method != "DELETE" and "--input" not in args):
    sys.exit("unexpected GitHub operation")
if method == "DELETE":
    name = args[args.index("DELETE") + 1].rsplit("/", 1)[-1]
    if (root / "fail-delete").exists() and name == (root / "fail-delete").read_text():
        sys.exit("simulated label deletion failure")
    labels = [row for row in labels if row["name"] != name]
    (root / "labels.json").write_text(json.dumps(labels))
    sys.exit(0)
body = json.load(sys.stdin)
if method == "POST":
    if (root / "fail-label").exists() and body["name"] == (root / "fail-label").read_text():
        sys.exit("simulated label write failure")
    label = dict(id=max([row["id"] for row in labels], default=0) + 1, name=body["name"], description=body["description"], color=body["color"])
    labels.append(label)
else:
    old = args[args.index("PATCH") + 1].rsplit("/", 1)[-1]
    label = next(row for row in labels if row["name"] == old)
    label.update(name=body["new_name"], description=body["description"], color=body.get("color", label["color"]))
(root / "labels.json").write_text(json.dumps(labels))
print(json.dumps(label))
''')
        (self.mock / "gh").chmod(0o755)

        taxonomy_path = self.root / "config/taxonomy.json"
        taxonomy = json.loads(taxonomy_path.read_text())
        taxonomy["label_catalog"] = dict(repository="owner/repo", status="observed", observed_at="2026-01-01T00:00:00Z", labels=[dict(id=7, name="bug", description="Old", color="ff0000", guidance="Keep the old advice")], retired=[])
        taxonomy_path.write_text(json.dumps(taxonomy))

        args = ("--expected-repo", "owner/repo", "--label-id", "7", "--expected-name", "bug", "--expected-description", "Old", "--name", "defect", "--description", "New")
        plan = self.json_cli("label-definitions", *args)
        self.assertEqual(plan["repository"], "owner/repo")
        self.assertEqual(plan["current"]["name"], "bug")
        self.assertEqual(plan["proposed"]["color"], "ff0000")
        self.assertFalse(any("PATCH" in call or "POST" in call for call in self.calls()))

        (self.mock / "labels.json").write_text(json.dumps([dict(id=7, name="bug", description="Someone changed it", color="ff0000")]))
        self.run_cli("label-definitions", *args, "--apply", "--preview-sha256", plan["preview_sha256"], ok=False)
        self.assertFalse(any("PATCH" in call or "POST" in call for call in self.calls()))
        (self.mock / "labels.json").write_text(json.dumps([dict(id=7, name="bug", description="Old", color="ff0000")]))

        self.json_cli("label-definitions", *args, "--apply", "--preview-sha256", plan["preview_sha256"])
        catalog = json.loads(taxonomy_path.read_text())["label_catalog"]
        self.assertEqual(catalog["labels"][0]["name"], "defect")
        self.assertEqual(catalog["labels"][0]["guidance"], "Keep the old advice")

        create = ("--expected-repo", "owner/repo", "--name", "triage", "--description", "Ready to triage")
        preview = self.json_cli("label-definitions", *create)
        self.assertEqual(preview["proposed"]["color"], "ededed")
        self.json_cli("label-definitions", *create, "--apply", "--preview-sha256", preview["preview_sha256"])
        self.assertEqual(len(json.loads(taxonomy_path.read_text())["label_catalog"]["labels"]), 2)

        labels = json.loads((self.mock / "labels.json").read_text())
        labels.append(dict(id=99, name="BUG", description="Keep this description", color="123456"))
        (self.mock / "labels.json").write_text(json.dumps(labels))
        defaults = ("--expected-repo", "owner/repo", "--initialize-defaults")
        default_preview = self.json_cli("label-definitions", *defaults)
        self.assertEqual(default_preview["operation"], "initialize-defaults")
        self.assertEqual(len(default_preview["create"]), 7)
        self.assertNotIn("bug", [row["name"] for row in default_preview["create"]])
        self.assertTrue(all(row["description"] and len(row["color"]) == 6 for row in default_preview["create"]))
        writes_before = len([call for call in self.calls() if "POST" in call])
        labels[-1]["description"] = "Changed during review"
        (self.mock / "labels.json").write_text(json.dumps(labels))
        self.run_cli("label-definitions", *defaults, "--apply", "--preview-sha256", default_preview["preview_sha256"], ok=False)
        self.assertEqual(len([call for call in self.calls() if "POST" in call]), writes_before)
        labels[-1]["description"] = "Keep this description"
        (self.mock / "labels.json").write_text(json.dumps(labels))
        (self.mock / "fail-label").write_text("documentation")
        self.run_cli("label-definitions", *defaults, "--apply", "--preview-sha256", default_preview["preview_sha256"], ok=False)
        self.assertIn("enhancement", [row["name"] for row in json.loads(taxonomy_path.read_text())["label_catalog"]["labels"]])
        (self.mock / "fail-label").unlink()
        resumed_preview = self.json_cli("label-definitions", *defaults)
        self.assertEqual(len(resumed_preview["create"]), 6)
        applied = self.json_cli("label-definitions", *defaults, "--apply", "--preview-sha256", resumed_preview["preview_sha256"])
        self.assertEqual(len(applied["created"]), 6)
        saved_labels = json.loads((self.mock / "labels.json").read_text())
        self.assertEqual(next(row for row in saved_labels if row["name"] == "BUG")["description"], "Keep this description")
        self.assertEqual(len(json.loads(taxonomy_path.read_text())["label_catalog"]["labels"]), 10)
        no_change = self.json_cli("label-definitions", *defaults)
        self.assertEqual(no_change["create"], [])
        self.assertEqual(self.json_cli("label-definitions", *defaults, "--apply", "--preview-sha256", no_change["preview_sha256"])["created"], [])
        self.assertEqual(len([call for call in self.calls() if "POST" in call]), writes_before + 8)

        taxonomy = json.loads(taxonomy_path.read_text())
        bug = next(row for row in taxonomy["label_catalog"]["labels"] if row["name"] == "BUG")
        bug.update(description="Local bug guidance", color="112233", guidance="Keep this local guidance")
        enhancement = next(row for row in taxonomy["label_catalog"]["labels"] if row["name"] == "enhancement")
        ghost = dict(id=777, name="local-custom", description="Keep this custom label", color="abcdef", guidance="Local only")
        taxonomy["label_catalog"]["labels"] = [bug, enhancement, ghost]
        taxonomy_path.write_text(json.dumps(taxonomy))
        remote = json.loads((self.mock / "labels.json").read_text())
        remote.append(dict(id=199, name="remote-only", description="Remove me", color="ff0000"))
        (self.mock / "labels.json").write_text(json.dumps(remote))

        local_args = ("--expected-repo", "owner/repo", "--reconcile-local")
        taxonomy["label_catalog"]["status"] = "pending"
        taxonomy_path.write_text(json.dumps(taxonomy))
        self.run_cli("label-definitions", *local_args, ok=False)
        taxonomy["label_catalog"]["status"] = "observed"
        taxonomy_path.write_text(json.dumps(taxonomy))
        local_preview = self.json_cli("label-definitions", *local_args)
        self.assertEqual(local_preview["operation"], "reconcile-local")
        self.assertEqual([row["name"] for row in local_preview["create"]], ["local-custom"])
        self.assertEqual(local_preview["update"][0]["proposed"]["description"], "Local bug guidance")
        self.assertIn("remote-only", [row["name"] for row in local_preview["delete"]])
        self.assertFalse(any("DELETE" in call for call in self.calls()))
        taxonomy["label_catalog"]["labels"][0]["description"] = "Changed while previewing"
        taxonomy_path.write_text(json.dumps(taxonomy))
        self.run_cli("label-definitions", *local_args, "--apply", "--preview-sha256", local_preview["preview_sha256"], ok=False)
        self.assertFalse(any("DELETE" in call for call in self.calls()))
        taxonomy["label_catalog"]["labels"][0]["description"] = "Local bug guidance"
        taxonomy_path.write_text(json.dumps(taxonomy))
        changed_remote = [*remote, dict(id=200, name="added-during-preview", description="New", color="abc123")]
        (self.mock / "labels.json").write_text(json.dumps(changed_remote))
        self.run_cli("label-definitions", *local_args, "--apply", "--preview-sha256", local_preview["preview_sha256"], ok=False)
        self.assertFalse(any("DELETE" in call for call in self.calls()))
        (self.mock / "labels.json").write_text(json.dumps(remote))

        (self.mock / "fail-delete").write_text(local_preview["delete"][0]["name"])
        self.run_cli("label-definitions", *local_args, "--apply", "--preview-sha256", local_preview["preview_sha256"], ok=False)
        self.assertEqual([row["name"] for row in json.loads(taxonomy_path.read_text())["label_catalog"]["labels"]], ["BUG", "enhancement", "local-custom"])
        (self.mock / "fail-delete").unlink()
        resumed_local = self.json_cli("label-definitions", *local_args)
        self.assertEqual(resumed_local["create"], [])
        self.assertEqual(resumed_local["update"], [])
        self.assertEqual(len(resumed_local["delete"]), len(local_preview["delete"]))
        self.json_cli("label-definitions", *local_args, "--apply", "--preview-sha256", resumed_local["preview_sha256"])
        final_remote = json.loads((self.mock / "labels.json").read_text())
        self.assertEqual({row["name"] for row in final_remote}, {"BUG", "enhancement", "local-custom"})
        self.assertEqual(next(row for row in final_remote if row["name"] == "BUG")["color"], "112233")
        final_local = json.loads(taxonomy_path.read_text())["label_catalog"]["labels"]
        self.assertEqual(next(row for row in final_local if row["name"] == "local-custom")["guidance"], "Local only")

        before = len(self.calls())
        self.json_cli("taxonomy-settings", "create-action", "--expected-repo", "owner/repo", "--name", "ask-review", "--description", "Ask a maintainer", "--operation", "comment")
        self.run_cli("taxonomy-settings", "update-action", "--expected-repo", "owner/repo", "--action", "ask-review", "--expected", "Ask a maintainer", "--expected-operation", "close", "--name", "request-review", "--description", "Ask a maintainer to review", "--operation", "comment", ok=False)
        self.json_cli("taxonomy-settings", "update-action", "--expected-repo", "owner/repo", "--action", "ask-review", "--expected", "Ask a maintainer", "--expected-operation", "comment", "--name", "request-review", "--description", "Ask a maintainer to review", "--operation", "comment")
        self.assertEqual(len(self.calls()), before)
        saved = json.loads(taxonomy_path.read_text())
        self.assertIn("request-review", saved["actions"])
        self.assertNotIn("ask-review", saved["actions"])
        self.assertEqual(saved["action_guidance"]["request-review"], "Ask a maintainer to review")
        self.assertEqual(saved["action_operations"]["request-review"], "comment")


class LabelApplicationTests(Workspace):
    def test_action_modes_are_explicit_per_repository_and_reset_after_guidance_changes(self):
        args = ("--expected-repo", "owner/repo")
        initial = self.json_cli("action-policy", "status", *args)
        modes = {row["name"]: row for row in initial["actions"]}
        self.assertFalse(modes["none"]["writable"])
        self.assertTrue(all(row["mode"] == "stage" for row in modes.values()))
        self.assertFalse(initial["pending_review_hold"])
        self.assertFalse((self.root / "config/action-automation.json").exists())

        hold = self.json_cli("action-policy", "hold", *args, "--enabled", "on")
        self.assertEqual((hold["before"], hold["after"]), (False, True))
        self.json_cli("action-policy", "hold", *args, "--enabled", "on", "--apply", "--preview-sha256", hold["preview_sha256"])
        self.assertTrue(self.json_cli("action-policy", "status", *args)["pending_review_hold"])

        proposal = self.json_cli("action-policy", "set", *args, "--action", "comment", "--mode", "execute")
        self.assertEqual(proposal["before"], "stage")
        self.assertEqual({row["name"]: row["mode"] for row in self.json_cli("action-policy", "status", *args)["actions"]}["comment"], "stage")
        self.run_cli("action-policy", "set", *args, "--action", "close", "--mode", "execute", "--apply", "--preview-sha256", proposal["preview_sha256"], ok=False)
        self.json_cli("action-policy", "set", *args, "--action", "comment", "--mode", "execute", "--apply", "--preview-sha256", proposal["preview_sha256"])
        modes = {row["name"]: row for row in self.json_cli("action-policy", "status", *args)["actions"]}
        self.assertEqual(modes["comment"]["mode"], "execute")
        self.assertEqual(modes["close"]["mode"], "stage")

        taxonomy_path = self.root / "config/taxonomy.json"
        taxonomy = json.loads(taxonomy_path.read_text())
        taxonomy["action_guidance"]["comment"] = "Ask only for missing reproduction details"
        taxonomy_path.write_text(json.dumps(taxonomy))
        changed = {row["name"]: row for row in self.json_cli("action-policy", "status", *args)["actions"]}
        self.assertEqual(changed["comment"]["mode"], "stage")
        self.assertTrue(changed["comment"]["stale_setting"])
        self.run_cli("action-policy", "set", *args, "--action", "comment", "--mode", "execute", "--apply", "--preview-sha256", proposal["preview_sha256"], ok=False)

        (self.root / "config/repo").write_text("other/repo\n")
        other = self.json_cli("action-policy", "status", "--expected-repo", "other/repo")
        self.assertTrue(all(row["mode"] == "stage" for row in other["actions"]))
        self.assertFalse(other["pending_review_hold"])
        self.run_cli("action-policy", "status", *args, ok=False)

    def test_enabled_bounded_pass_revalidates_and_respects_human_corrections(self):
        (self.mock / "live.json").write_text(json.dumps(dict(number=1, state="open", labels=[])))
        (self.mock / "gh").write_text('''#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
root = Path(os.environ["FAKE_GH_DIR"])
args = sys.argv[1:]
with (root / "calls.jsonl").open("a") as out:
    out.write(json.dumps(args) + "\\n")
method = args[args.index("--method") + 1]
endpoint = args[args.index("--method") + 2]
live = json.loads((root / "live.json").read_text())
if endpoint == "repos/owner/repo/issues/1" and method == "GET":
    print(json.dumps(live))
    sys.exit(0)
if endpoint == "repos/owner/repo/issues/2" and method == "GET":
    print(json.dumps(dict(number=2, state="open", labels=[])))
    sys.exit(0)
if endpoint == "repos/owner/repo/issues/1/labels" and method == "POST":
    names = json.load(sys.stdin)["labels"]
    for name in names:
        if name not in [label["name"] for label in live["labels"]]:
            live["labels"].append(dict(name=name))
elif endpoint.startswith("repos/owner/repo/issues/1/labels/") and method == "DELETE":
    name = endpoint.rsplit("/", 1)[-1]
    live["labels"] = [label for label in live["labels"] if label["name"] != name]
else:
    sys.exit("unexpected GitHub operation")
(root / "live.json").write_text(json.dumps(live))
if method == "POST" and (root / "uncertain").exists():
    print("{invalid response")
    sys.exit(0)
print(json.dumps(live["labels"]))
''')
        (self.mock / "gh").chmod(0o755)

        taxonomy_path = self.root / "config/taxonomy.json"
        taxonomy = json.loads(taxonomy_path.read_text())
        taxonomy["label_catalog"] = dict(repository="owner/repo", status="observed", observed_at="2026-01-01T00:00:00Z", labels=[dict(id=7, name="bug", description="Defect", color="ff0000")], retired=[])
        taxonomy_path.write_text(json.dumps(taxonomy))
        ledger_path = self.root / "data/owner/repo/ledger.jsonl"
        first = dict(item(1, "Bug"), proposed_labels=["bug"], action="", confidence="high", reason="Agent proposal")
        second = dict(item(2, "Unlabeled"), proposed_labels=["bug"], action="", confidence="medium", reason="Agent proposal")
        first["labels"] = ["manual"]
        ledger_path.write_text(json.dumps(first) + "\n" + json.dumps(second) + "\n")

        self.assertTrue(self.json_cli("item-labels", "status", "--expected-repo", "owner/repo")["enabled"])
        self.assertFalse((self.root / "config/label-application.json").exists())
        disable = self.json_cli("item-labels", "disable", "--expected-repo", "owner/repo")
        self.assertTrue(disable["before"])
        self.json_cli("item-labels", "disable", "--expected-repo", "owner/repo", "--apply", "--preview-sha256", disable["preview_sha256"])
        self.assertFalse(self.json_cli("item-labels", "status", "--expected-repo", "owner/repo")["enabled"])
        enable = self.json_cli("item-labels", "enable", "--expected-repo", "owner/repo")
        self.assertFalse(enable["before"])
        self.json_cli("item-labels", "enable", "--expected-repo", "owner/repo", "--apply", "--preview-sha256", enable["preview_sha256"])

        initial = self.json_cli("item-labels", "preview", "--expected-repo", "owner/repo", "--limit", "1")
        self.assertEqual(initial["items"][0]["number"], 2)
        first["labels"] = []
        ledger_path.write_text(json.dumps(first) + "\n")

        preview = self.json_cli("item-labels", "preview", "--expected-repo", "owner/repo", "--limit", "1")
        self.assertEqual(preview["items"][0]["add"], ["bug"])
        self.assertFalse(any("POST" in call for call in self.calls()))
        (self.mock / "live.json").write_text(json.dumps(dict(number=1, state="open", labels=[dict(name="manual")])))
        self.run_cli("item-labels", "run", "--expected-repo", "owner/repo", "--limit", "1", "--preview-sha256", preview["preview_sha256"], ok=False)
        self.assertFalse(any("POST" in call for call in self.calls()))
        (self.mock / "live.json").write_text(json.dumps(dict(number=1, state="open", labels=[])))
        preview = self.json_cli("item-labels", "preview", "--expected-repo", "owner/repo", "--limit", "1")
        result = self.json_cli("item-labels", "run", "--expected-repo", "owner/repo", "--limit", "1", "--preview-sha256", preview["preview_sha256"])
        self.assertEqual(result["outcomes"][0]["status"], "written")
        self.assertEqual(json.loads((self.mock / "live.json").read_text())["labels"], [dict(name="bug")])
        self.assertEqual(json.loads((self.root / "data/owner/repo/local/item-labels-state.json").read_text())["items"]["issue:1"]["managed"], ["bug"])

        row = json.loads(ledger_path.read_text())
        row["proposed_labels"] = []
        ledger_path.write_text(json.dumps(row) + "\n")
        removal = self.json_cli("item-labels", "preview", "--expected-repo", "owner/repo", "--limit", "1")
        self.assertEqual(removal["items"][0]["remove"], ["bug"])
        self.json_cli("item-labels", "run", "--expected-repo", "owner/repo", "--limit", "1", "--preview-sha256", removal["preview_sha256"])
        self.assertEqual(json.loads((self.mock / "live.json").read_text())["labels"], [])
        row["proposed_labels"] = ["bug"]
        ledger_path.write_text(json.dumps(row) + "\n")
        again = self.json_cli("item-labels", "preview", "--expected-repo", "owner/repo", "--limit", "1")
        self.json_cli("item-labels", "run", "--expected-repo", "owner/repo", "--limit", "1", "--preview-sha256", again["preview_sha256"])

        (self.mock / "live.json").write_text(json.dumps(dict(number=1, state="open", labels=[])))
        corrected = self.json_cli("item-labels", "preview", "--expected-repo", "owner/repo", "--limit", "1")
        self.assertEqual(corrected["items"][0]["status"], "human-correction")
        calls = len(self.calls())
        self.json_cli("item-labels", "run", "--expected-repo", "owner/repo", "--limit", "1", "--preview-sha256", corrected["preview_sha256"])
        self.assertFalse(any("POST" in call for call in self.calls()[calls:]))
        paused = self.json_cli("item-labels", "preview", "--expected-repo", "owner/repo", "--limit", "1")
        self.assertEqual(paused["items"][0]["status"], "paused-after-correction")
        reset = self.json_cli("item-labels", "reset-item", "--expected-repo", "owner/repo", "--kind", "issue", "--number", "1")
        self.assertEqual(reset["before"]["managed"], ["bug"])
        self.json_cli("item-labels", "reset-item", "--expected-repo", "owner/repo", "--kind", "issue", "--number", "1", "--apply", "--preview-sha256", reset["preview_sha256"])
        renewed = self.json_cli("item-labels", "preview", "--expected-repo", "owner/repo", "--limit", "1")
        self.assertEqual(renewed["items"][0]["add"], ["bug"])
        (self.mock / "uncertain").write_text("simulate an ambiguous GitHub response")
        uncertain = self.json_cli("item-labels", "run", "--expected-repo", "owner/repo", "--limit", "1", "--preview-sha256", renewed["preview_sha256"])
        self.assertEqual(uncertain["outcomes"][0]["status"], "uncertain")
        self.assertIn("issue:1", self.json_cli("item-labels", "status", "--expected-repo", "owner/repo")["pending_items"])
        blocked = self.json_cli("item-labels", "preview", "--expected-repo", "owner/repo", "--limit", "1")
        self.assertEqual(blocked["items"][0]["status"], "uncertain-write")
        ledger_path.write_text(json.dumps(row) + "\n" + json.dumps(second) + "\n")
        next_item = self.json_cli("item-labels", "preview", "--expected-repo", "owner/repo", "--limit", "1")
        self.assertEqual(next_item["items"][0]["number"], 2)
        scoped = self.json_cli("item-labels", "preview", "--expected-repo", "owner/repo", "--limit", "1", "--key", "issue:2")
        self.assertEqual(scoped["selected_keys"], ["issue:2"])
        self.run_cli("item-labels", "run", "--expected-repo", "owner/repo", "--limit", "1", "--key", "issue:1", "--preview-sha256", scoped["preview_sha256"], ok=False)
        self.assertEqual(json.loads((self.mock / "live.json").read_text())["labels"], [dict(name="bug")])


class LedgerTests(Workspace):
    def setUp(self):
        super().setUp()
        rows = [item(1, "Suspend crashes on NVIDIA"), item(2, "NVIDIA suspend crash")]
        self.responses["repos/owner/repo/issues?state=open&per_page=100"] = dict(data=rows)

    def sync(self):
        self.run_cli("fetch")
        self.run_cli("sync")

    def test_brief_reader_groups_files_and_preserves_item_markdown(self):
        self.sync()
        reports = self.root / "reports/owner/repo"
        reports.mkdir(parents=True)
        files = {
            "2026-10-06-master-brief.md": "# Master decisions\n",
            "2026-10-06-pr-42-brief.md": "# PR 42\n\n**Recommendation:** Review\n\n```go\nreturn true\n```\n",
            "2026-10-05-group-related-brief.md": "# Related reports\n",
            "2026-10-06-batch-b20261006-000001-brief.md": "# Recent batch\n",
            "2026-10-06.md": "# Generated report\n",
        }
        for name, content in files.items():
            (reports / name).write_text(content)

        outside = self.root / "outside.md"
        outside.write_text("# Foreign brief\n")
        (reports / "2026-10-06-issue-99-brief.md").symlink_to(outside)
        calls_before = len(self.calls())
        rows = self.json_cli("briefs", "--expected-repo", "owner/repo", "list")["records"]
        self.assertEqual([(row["section"], row["type"]) for row in rows],
                         [("master", "master"), ("item", "pr"), ("work", "group"), ("work", "batch")])
        brief = self.json_cli("briefs", "--expected-repo", "owner/repo", "read", "2026-10-06-pr-42-brief.md")
        self.assertIn("```go\nreturn true\n```", brief["content"])
        named = self.json_cli("briefs", "plan", "--brief", "2026-10-06-pr-42-brief.md", "--brief", "2026-10-05-group-related-brief.md", "--brief", "2026-10-06-batch-b20261006-000001-brief.md")
        self.assertEqual((named["source_count"], named["target_decisions"]), (3, 6))
        sources = [row["id"] for row in named["briefs"]]
        record_args = ("--expected-repo", "owner/repo", "record-master", "--master", "2026-10-06-master-brief.md",
                       "--plan-sha256", named["plan_sha256"], "--by", "agent:tester")
        self.assertIn("source briefs changed", self.run_cli("briefs", *record_args, *(part for source in sources for part in ("--brief", source)),
                                                             "--plan-sha256", "0" * 64, ok=False).stderr)
        recorded = self.json_cli("briefs", *record_args, *(part for source in sources for part in ("--brief", source)))
        self.assertEqual(recorded["sources"], named["briefs"])
        self.assertEqual(recorded["master_sha256"], hashlib.sha256((reports / "2026-10-06-master-brief.md").read_bytes()).hexdigest())
        self.assertEqual(self.json_cli("briefs", *record_args, *(part for source in sources for part in ("--brief", source))), recorded)
        (reports / sources[0]).write_text("# Edited source\n")
        self.assertIn("source briefs changed", self.run_cli("briefs", *record_args, *(part for source in sources for part in ("--brief", source)), ok=False).stderr)
        self.assertEqual(json.loads((self.root / "data/owner/repo/master-briefs/2026-10-06-master-brief.json").read_text()), recorded)
        self.assertEqual(self.json_cli("briefs", "plan", "--brief", "reports/owner/repo/2026-10-06-pr-42-brief.md")["source_count"], 1)
        self.run_cli("briefs", "plan", "--brief", "reports/other/repo/2026-10-06-pr-42-brief.md", ok=False)
        for index in range(2, 5):
            (reports / f"2026-10-06-batch-b20261006-{index:06d}-brief.md").write_text(f"# Batch {index}\n")
        all_batches = self.json_cli("briefs", "plan", "--all")
        self.assertEqual((all_batches["source_count"], all_batches["target_decisions"]), (4, 6))
        self.assertEqual(self.json_cli("briefs", "plan", "--limit", "2")["target_decisions"], 4)
        for index in range(5, 17):
            (reports / f"2026-10-06-batch-b20261006-{index:06d}-brief.md").write_text(f"# Batch {index}\n")
        large = self.json_cli("briefs", "plan", "--all")
        self.assertEqual((large["source_count"], large["target_decisions"]), (16, 10))
        batch_name = "2026-10-06-batch-b20261006-000016-brief.md"
        batch_preview = self.json_cli("briefs", "mark-read", batch_name)
        self.json_cli("briefs", "--expected-repo", "owner/repo", "mark-read", "--apply", "--preview-sha256", batch_preview["preview_sha256"], batch_name)
        self.assertEqual(self.json_cli("briefs", "plan", "--all")["source_count"], 15)
        self.assertEqual(self.json_cli("briefs", "plan", "--all", "--include-read")["source_count"], 16)
        self.assertTrue((reports / (Path(batch_name).stem + "_READ.md")).exists())

        pr_name = "2026-10-06-pr-42-brief.md"
        preview = self.json_cli("briefs", "mark-read", pr_name)
        self.assertFalse(preview["applied"])
        self.assertEqual(preview["renames"][0]["target"], Path(pr_name).stem + "_READ.md")
        self.assertTrue((reports / pr_name).exists())
        (reports / pr_name).write_text(brief["content"] + "Changed after preview.\n")
        self.run_cli("briefs", "--expected-repo", "owner/repo", "mark-read", "--apply", "--preview-sha256", preview["preview_sha256"], pr_name, ok=False)
        self.assertTrue((reports / pr_name).exists())
        preview = self.json_cli("briefs", "mark-read", pr_name)
        self.run_cli("briefs", "--expected-repo", "other/repo", "mark-read", "--apply", "--preview-sha256", preview["preview_sha256"], pr_name, ok=False)
        applied = self.json_cli("briefs", "--expected-repo", "owner/repo", "mark-read", "--apply", "--preview-sha256", preview["preview_sha256"], pr_name)
        self.assertTrue(applied["applied"])
        self.assertFalse((reports / pr_name).exists())
        self.assertIn("Changed after preview.", (reports / (Path(pr_name).stem + "_READ.md")).read_text())
        self.assertFalse(any(row["id"] == pr_name for row in self.json_cli("briefs", "list")["records"]))
        self.assertEqual(self.json_cli("briefs", "plan", "--brief", Path(pr_name).stem + "_READ.md")["source_count"], 1)
        self.run_cli("briefs", "read", Path(pr_name).stem + "_READ.md", ok=False)

        group_name = "2026-10-05-group-related-brief.md"
        (reports / (Path(group_name).stem + "_READ.md")).write_text("already marked\n")
        self.run_cli("briefs", "mark-read", group_name, ok=False)
        self.run_cli("briefs", "plan", "--brief", group_name, "--brief", Path(group_name).stem + "_READ.md", ok=False)
        self.assertTrue((reports / group_name).exists())
        self.assertEqual(len(self.calls()), calls_before)
        self.run_cli("briefs", "--expected-repo", "other/repo", "list", ok=False)
        self.run_cli("briefs", "read", "2026-10-06-issue-99-brief.md", ok=False)
        self.run_cli("apply", "--number", "1", "--kind", "issue", "--action", "none", "--reason", "Needs review", "--by", "agent:triage")
        self.json_cli("review-request", "mark", "--expected-repo", "owner/repo", "--kind", "issue", "--number", "1", "--by", "agent:triage", "--reason", "Important project decision")
        before = self.json_cli("next", "--json")["suggestions"]
        self.assertTrue(any("Write a detailed brief for issue #1" in row["what"] for row in before))
        (reports / "2026-10-06-issue-1-brief.md").write_text("# Issue 1\n")
        after = self.json_cli("next", "--json")["suggestions"]
        self.assertFalse(any("Write a detailed brief for issue #1" in row["what"] for row in after))
        issue_name = "2026-10-06-issue-1-brief.md"
        preview = self.json_cli("briefs", "mark-read", issue_name)
        self.json_cli("briefs", "--expected-repo", "owner/repo", "mark-read", "--apply", "--preview-sha256", preview["preview_sha256"], issue_name)
        archived = self.json_cli("next", "--json")["suggestions"]
        self.assertFalse(any("Write a detailed brief for issue #1" in row["what"] for row in archived))

    def test_fetch_sync_and_agent_proposal(self):
        self.sync()
        self.assertEqual(len(self.ledger()), 2)
        self.assertTrue(all(row["review_request"] is None for row in self.ledger().values()))

        ledger_path = self.root / "data/owner/repo/ledger.jsonl"
        rows = [json.loads(line) for line in ledger_path.read_text().splitlines()]
        rows[0]["labels"] = ["existing"]
        ledger_path.write_text("".join(json.dumps(row) + "\n" for row in rows))
        (self.mock / "gh").write_text('''#!/usr/bin/env python3
import json, sys
if sys.argv[1:3] != ["issue", "view"] or sys.argv[3] not in ("1", "2"):
    sys.exit("unexpected GitHub operation")
print(json.dumps(dict(title="Issue " + sys.argv[3], body="Details", comments=[], state="CLOSED")))
''')
        (self.mock / "gh").chmod(0o755)
        self.run_cli("batch", "1", "--unlabeled-first")
        batch = next((self.root / "data/owner/repo/batches").glob("*.items.jsonl"))
        self.assertEqual(json.loads(batch.read_text().splitlines()[0])["number"], 2)
        self.assertEqual(self.json_cli("enrich-one", "--kind", "issue", "--number", "2")["state"], "closed")

        before = ledger_path.read_bytes()
        args = ("--number", "1", "--kind", "issue", "--action", "none", "--reason", "Reproduced", "--by", "agent:triage")
        self.run_cli("apply", *args, "--dry-run")
        self.assertEqual(ledger_path.read_bytes(), before)
        self.run_cli("apply", *args)
        row = self.ledger()[("issue", 1)]
        self.assertEqual(row["triaged_by"], "agent:triage")
        self.assertIsNone(row["review_request"])
        before_batches = set((self.root / "data/owner/repo/batches").glob("*.items.jsonl"))
        self.run_cli("batch", "2", "--include-triaged", "--unlabeled-first")
        newest = next(iter(set((self.root / "data/owner/repo/batches").glob("*.items.jsonl")) - before_batches))
        self.assertEqual({json.loads(line)["number"] for line in newest.read_text().splitlines()}, {1, 2})
        rows = [json.loads(line) for line in ledger_path.read_text().splitlines()]
        for current in rows:
            current["updated_at"] = "2026-10-06T00:00:00Z" if current["number"] == 1 else "2026-09-22T00:00:00Z"

        ledger_path.write_text("".join(json.dumps(current) + "\n" for current in rows))
        before_updated = set((self.root / "data/owner/repo/batches").glob("*.items.jsonl"))
        self.run_cli("batch", "1", "--include-triaged", "--order", "updated")
        updated = next(iter(set((self.root / "data/owner/repo/batches").glob("*.items.jsonl")) - before_updated))
        self.assertEqual(json.loads(updated.read_text().splitlines()[0])["number"], 1)

        brief = self.root / "reports/owner/repo" / f"2026-10-06-batch-{batch.name.removesuffix('.items.jsonl')}-brief.md"
        brief.parent.mkdir(parents=True, exist_ok=True)
        brief.write_text("# Screened batch\n")
        self.run_cli("batch", "--mark-briefed", batch.name.removesuffix(".items.jsonl"), "--brief", str(brief))
        self.run_cli("batch", "--remove", batch.name.removesuffix(".items.jsonl"), "--key", "issue:2", ok=False)
        self.run_cli("batch", "--delete", batch.name.removesuffix(".items.jsonl"))
        existing = set((self.root / "data/owner/repo/batches").glob("*.items.jsonl"))
        next_batch = self.run_cli("batch", "2", "--include-triaged", "--unbriefed")
        self.assertIn("1 items", next_batch.stdout)
        new_items = next(iter(set((self.root / "data/owner/repo/batches").glob("*.items.jsonl")) - existing))
        screened = json.loads(new_items.read_text().splitlines()[0])
        self.assertEqual(screened["number"], 1)
        self.assertTrue(screened["briefing_batch"])
        next_steps = self.json_cli("next", "--json")["suggestions"]
        self.assertTrue(any(f"Brief screening batch {new_items.name.removesuffix('.items.jsonl')}" in row["what"] for row in next_steps), next_steps)

    def test_finished_batches_need_briefs_before_next_suggests_deletion_and_polish(self):
        self.sync()
        batches = self.root / "data/owner/repo/batches"
        reports = self.root / "reports/owner/repo"
        batches.mkdir(parents=True, exist_ok=True)
        reports.mkdir(parents=True, exist_ok=True)

        for number in (1, 2):
            batch_id = f"b20261006-00000{number}"
            (batches / f"{batch_id}.items.jsonl").write_text(json.dumps(dict(kind="issue", number=number)) + "\n")
            (batches / f"{batch_id}.decisions.jsonl").write_text("")
            self.run_cli("apply", "--number", str(number), "--kind", "issue", "--action", "none",
                         "--reason", "Assessed", "--by", "agent:triage")

        first = self.json_cli("next", "--json")["suggestions"]
        self.assertTrue(any("Brief finished batch b20261006-000001" in row["what"] for row in first))
        self.assertFalse(any("Delete finished batch b20261006-000001" in row["what"] for row in first))
        missing = reports / "2026-10-06-batch-b20261006-000001-brief.md"
        self.run_cli("batch", "--mark-briefed", "b20261006-000001", "--brief", str(missing), ok=False)
        self.assertFalse((self.root / "data/owner/repo/briefed-batches/b20261006-000001.json").exists())

        for number in (1, 2):
            brief = reports / f"2026-10-06-batch-b20261006-00000{number}-brief.md"
            brief.write_text("# Batch brief\n")
            self.run_cli("batch", "--mark-briefed", f"b20261006-00000{number}", "--brief", str(brief))

        (reports / f"{datetime.now(timezone.utc).date().isoformat()}.md").write_text("# Report\n")
        second = self.json_cli("next", "--json")["suggestions"]
        self.assertTrue(any("Delete finished batch b20261006-000001" in row["what"] for row in second))
        self.assertTrue(any("Polish selected batch briefs (2 available)" in row["what"] for row in second))
        self.assertFalse(any("Write today's maintainer report" in row["what"] for row in second))
        batch_name = "2026-10-06-batch-b20261006-000001-brief.md"
        preview = self.json_cli("briefs", "mark-read", batch_name)
        self.json_cli("briefs", "--expected-repo", "owner/repo", "mark-read", "--apply", "--preview-sha256", preview["preview_sha256"], batch_name)
        after_read = self.json_cli("next", "--json")["suggestions"]
        self.assertTrue(any("Delete finished batch b20261006-000001" in row["what"] for row in after_read))
        self.assertFalse(any("Polish selected batch briefs" in row["what"] for row in after_read))
        self.run_cli("batch", "--mark-briefed", "b20261006-000001", "--brief", str(reports / batch_name))
        self.assertEqual(self.json_cli("briefs", "plan", "--all", "--include-read")["source_count"], 2)

        (reports / "2026-10-06-batch-b20261006-000003-brief.md").write_text("# Later batch brief\n")
        master_name = f"{datetime.now(timezone.utc).date().isoformat()}-master-brief.md"
        (reports / master_name).write_text("# Master brief\n")
        preview = self.json_cli("briefs", "mark-read", master_name)
        self.json_cli("briefs", "--expected-repo", "owner/repo", "mark-read", "--apply", "--preview-sha256", preview["preview_sha256"], master_name)
        self.assertFalse(any("Polish selected batch briefs" in row["what"] for row in self.json_cli("next", "--json")["suggestions"]))

    def test_sync_preserves_proposals(self):
        self.sync()
        self.run_cli("apply", "--number", "1", "--kind", "issue", "--action", "none", "--confidence", "high", "--reason", "Existing call", "--by", "agent:triage")
        taxonomy_path = self.root / "config/taxonomy.json"
        taxonomy = json.loads(taxonomy_path.read_text())
        taxonomy["label_catalog"] = dict(repository="owner/repo", status="observed", observed_at="2026-01-01T00:00:00Z", labels=[dict(id=7, name="bug", description="Defect", color="ff0000")], retired=[])
        taxonomy_path.write_text(json.dumps(taxonomy))
        self.run_cli("apply", "--number", "2", "--kind", "issue", "--proposed-label", "bug", "--by", "agent:triage")
        self.assertEqual(self.ledger()[("issue", 2)]["proposed_labels"], ["bug"])
        self.assertEqual(self.ledger()[("issue", 2)]["action"], "")
        self.assertEqual(self.ledger()[("issue", 2)]["labels"], [])
        self.run_cli("fetch", "--full")
        self.run_cli("sync")
        self.assertEqual(self.ledger()[("issue", 1)]["action"], "none")
        self.assertIsNone(self.ledger()[("issue", 1)]["review_request"])
        self.assertEqual(self.ledger()[("issue", 2)]["proposed_labels"], ["bug"])
        self.assertIn("Triaged: 2/2", self.run_cli("stats").stdout)
        report = self.run_cli("report", "--stdout").stdout
        self.assertIn("Issue proposed labels", report)
        self.assertIn("| bug | 1 |", report)
        self.json_cli("review-request", "mark", "--expected-repo", "owner/repo", "--kind", "issue", "--number", "2", "--by", "agent:triage", "--reason", "Check the reported behavior")
        self.assertEqual(self.ledger()[("issue", 2)]["review_request"]["reason"], "Check the reported behavior")
        self.run_cli("apply", "--number", "2", "--kind", "issue", "--replace-proposed-labels", "--action", "none", "--by", "human")
        self.assertEqual(self.ledger()[("issue", 2)]["proposed_labels"], [])
        self.assertIsNotNone(self.ledger()[("issue", 2)]["review_request"])
        self.run_cli("apply", "--number", "1", "--kind", "issue", "--proposed-label", "bug", "--by", "agent:labels")
        self.assertEqual(self.ledger()[("issue", 1)]["action"], "none")
        self.assertEqual(self.ledger()[("issue", 1)]["proposed_labels"], ["bug"])
        self.assertEqual(self.ledger()[("issue", 1)]["confidence"], "high")
        self.assertEqual(self.ledger()[("issue", 1)]["reason"], "Existing call")
        self.assertIsNone(self.ledger()[("issue", 1)]["review_request"])

        request = self.ledger()[("issue", 2)]["review_request"]
        self.run_cli("review-request", "clear", "--expected-repo", "owner/repo", "--kind", "issue", "--number", "2", "--by", "agent:other", ok=False)
        self.run_cli("review-request", "clear", "--expected-repo", "owner/repo", "--kind", "issue", "--number", "2", "--by", "human", "--expected-by", request["by"], "--expected-at", request["at"], "--expected-reason", "stale reason", ok=False)
        self.json_cli("review-request", "clear", "--expected-repo", "owner/repo", "--kind", "issue", "--number", "2", "--by", "human", "--expected-by", request["by"], "--expected-at", request["at"], "--expected-reason", request["reason"])
        self.assertIsNone(self.ledger()[("issue", 2)]["review_request"])
        self.assertEqual(self.ledger()[("issue", 2)]["action"], "none")

        ledger_path = self.root / "data/owner/repo/ledger.jsonl"
        rows = [json.loads(line) for line in ledger_path.read_text().splitlines()]
        rows[1]["review_request"] = {}
        ledger_path.write_text("".join(json.dumps(row) + "\n" for row in rows))
        before = ledger_path.read_bytes()
        self.run_cli("review-request", "clear", "--expected-repo", "owner/repo", "--kind", "issue", "--number", "2", "--by", "human", ok=False)
        self.assertEqual(ledger_path.read_bytes(), before)

    def test_csv_decision_revisions_and_explicit_review_request(self):
        self.sync()
        ledger_path = self.root / "data/owner/repo/ledger.jsonl"
        self.run_cli("export-csv")
        csv_path = next((self.root / "data/owner/repo/exports").glob("ledger-*.csv"))

        with csv_path.open(newline="") as source:
            reader = csv.DictReader(source)
            fields, rows = reader.fieldnames, list(reader)

        rows[0].update(action="none", maintainer_notes="Check before changing the default")
        with csv_path.open("w", newline="") as out:
            writer = csv.DictWriter(out, fieldnames=fields)
            writer.writeheader()
            writer.writerows(rows)

        self.run_cli("import-csv", str(csv_path), "--by", "operator")
        imported = self.ledger()[("issue", 1)]
        self.assertIsNone(imported["review_request"])
        self.assertEqual(imported["maintainer_notes"], "Check before changing the default")
        self.assertEqual(imported["triaged_by"], "operator")

        self.json_cli("review-request", "mark", "--expected-repo", "owner/repo", "--kind", "issue", "--number", "1", "--by", "agent:triage", "--reason", "Project-wide default needs attention")
        self.run_cli("export-csv", "--pending-review")
        with csv_path.open(newline="") as source:
            self.assertEqual(len(list(csv.DictReader(source))), 1)

        self.run_cli("export-csv")
        with csv_path.open(newline="") as source:
            reader = csv.DictReader(source)
            fields, rows = reader.fieldnames, list(reader)
        ledger_rows = [json.loads(line) for line in ledger_path.read_text().splitlines()]
        ledger_rows[0]["last_synced_at"] = "2026-01-01T00:00:00Z"
        ledger_path.write_text("".join(json.dumps(row) + "\n" for row in ledger_rows))
        before = ledger_path.read_bytes()
        self.run_cli("import-csv", str(csv_path))
        self.assertEqual(ledger_path.read_bytes(), before)

        rows[0]["reason"] = "New rationale"
        with csv_path.open("w", newline="") as out:
            writer = csv.DictWriter(out, fieldnames=fields)
            writer.writeheader()
            writer.writerows(rows)
        self.run_cli("import-csv", str(csv_path), "--by", "operator")
        changed = self.ledger()[("issue", 1)]
        self.assertEqual(changed["review_request"]["reason"], "Project-wide default needs attention")

        self.run_cli("export-csv")
        with csv_path.open(newline="") as source:
            reader = csv.DictReader(source)
            fields, rows = reader.fieldnames, list(reader)
        rows[0]["maintainer_notes"] = "Must not publish"
        rows[1]["maintainer_notes"] = "Keep this"
        with csv_path.open("w", newline="") as out:
            writer = csv.DictWriter(out, fieldnames=fields)
            writer.writeheader()
            writer.writerows(rows)
        self.run_cli("apply", "--number", "2", "--kind", "issue", "--action", "none", "--by", "agent:triage")
        before = ledger_path.read_bytes()
        self.run_cli("import-csv", str(csv_path), ok=False)
        self.assertEqual(ledger_path.read_bytes(), before)

    def test_ledger_writers_serialize_complete_transactions(self):
        self.responses["repos/owner/repo/issues?state=open&per_page=100"]["data"].append(item(3, "Another issue"))
        self.sync()
        self.run_cli("export-csv")
        calls_before = self.calls()
        data = self.root / "data/owner/repo"
        ledger_path = data / "ledger.jsonl"
        lock_path = data / "local/ledger.jsonl.lock"

        storage = self.root / "bin/_storage.py"
        source = storage.read_text()
        self.assertIn("        fcntl.flock(lock, fcntl.LOCK_EX)", source)
        source = source.replace(
            "        fcntl.flock(lock, fcntl.LOCK_EX)",
            "        if path.name == 'ledger.jsonl':\n            os.write(int(os.environ['LEDGER_TEST_NOTIFY_FD']), b'L')\n        fcntl.flock(lock, fcntl.LOCK_EX)",
        )
        storage.write_text(source)
        triage = self.root / "bin/_triage.py"
        source = triage.read_text()
        self.assertIn("def load_ledger():\n    records = load_jsonl(LEDGER_PATH)", source)
        source = source.replace(
            "def load_ledger():\n    records = load_jsonl(LEDGER_PATH)",
            "def load_ledger():\n    os.write(int(os.environ['LEDGER_TEST_NOTIFY_FD']), b'R')\n    records = load_jsonl(LEDGER_PATH)",
        )
        triage.write_text(source)

        raw_path = data / "raw/issues_and_prs.jsonl"
        rows = [json.loads(line) for line in raw_path.read_text().splitlines()]
        rows[0]["title"] = "Fresh title"
        raw_path.write_text("".join(json.dumps(row) + "\n" for row in rows))
        meta_path = data / "raw/fetch_meta.json"
        meta = json.loads(meta_path.read_text())
        meta["raw_sha256"] = hashlib.sha256(raw_path.read_bytes()).hexdigest()
        meta_path.write_text(json.dumps(meta))
        exported = next((data / "exports").glob("ledger-*.csv"))
        csv_path = self.root / "notes.csv"
        with exported.open(newline="") as source, csv_path.open("w", newline="") as out:
            reader = csv.DictReader(source)
            writer = csv.DictWriter(out, fieldnames=reader.fieldnames)
            writer.writeheader()
            row = next(row for row in reader if row["kind"] == "issue" and row["number"] == "3")
            row["maintainer_notes"] = "Separate note"
            writer.writerow(row)

        read_fd, write_fd = os.pipe()
        commands = [
            ["apply", "--number", "1", "--kind", "issue", "--action", "none"],
            ["apply", "--number", "2", "--kind", "issue", "--action", "none"],
            ["sync"],
            ["import-csv", str(csv_path)],
        ]
        processes = []
        try:
            with lock_path.open("a") as lock:
                fcntl.flock(lock, fcntl.LOCK_EX)
                for args in commands:
                    env = dict(self.env, TRIAGE_ROOT=str(self.root), LEDGER_TEST_NOTIFY_FD=str(write_fd))
                    processes.append(subprocess.Popen([str(self.root / "bin" / args[0]), *args[1:]], cwd=self.root, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, pass_fds=(write_fd,)))

                reached = bytearray()
                while len(reached) < len(commands):
                    ready, _, _ = select.select([read_fd], [], [], 10)
                    self.assertTrue(ready, "ledger writer did not reach the lock")
                    reached.extend(os.read(read_fd, len(commands) - len(reached)))

                self.assertEqual(reached, b"L" * len(commands))
                ready, _, _ = select.select([read_fd], [], [], 0)
                self.assertFalse(ready, "a ledger read occurred while the lock was held")
                fcntl.flock(lock, fcntl.LOCK_UN)

            for process in processes:
                stdout, stderr = process.communicate(timeout=10)
                self.assertEqual(process.returncode, 0, stdout + stderr)
        finally:
            for process in processes:
                if process.poll() is None:
                    process.kill()
                process.communicate()
            os.close(read_fd)
            os.close(write_fd)

        ledger = self.ledger()
        self.assertEqual(ledger[("issue", 1)]["action"], "none")
        self.assertEqual(ledger[("issue", 2)]["action"], "none")
        self.assertEqual(ledger[("issue", 3)]["maintainer_notes"], "Separate note")
        self.assertEqual(ledger[("issue", 1)]["title"], "Fresh title")
        self.assertEqual(self.calls(), calls_before)

    def test_direct_item_read_overrides_lagging_issue_list_after_close(self):
        self.sync()
        meta = json.loads((self.root / "data/owner/repo/raw/fetch_meta.json").read_text())
        since = (datetime.strptime(meta["synced_through"], "%Y-%m-%dT%H:%M:%SZ").replace(tzinfo=timezone.utc) - timedelta(minutes=5)).strftime("%Y-%m-%dT%H:%M:%SZ")
        self.responses[f"repos/owner/repo/issues?state=all&since={since}&per_page=100"] = dict(data=[item(1, "Suspend crashes on NVIDIA")])
        self.responses["repos/owner/repo/issues/1"] = dict(data=dict(number=1, html_url="https://github.com/owner/repo/issues/1", state="closed", title="Suspend crashes on NVIDIA", user=dict(login="author"), created_at="2025-01-01T00:00:00Z", updated_at="2025-01-02T00:00:00Z", labels=[], comments=1))

        self.run_cli("fetch", "--include-item", "issue:1")
        self.run_cli("sync")
        self.assertEqual(self.ledger()[("issue", 1)]["state"], "closed")
        self.assertEqual(self.ledger()[("issue", 2)]["state"], "open")

    def test_direct_item_read_pins_enterprise_host_for_list_and_item(self):
        host = "ghe.example"
        row = item(1, "Enterprise issue")
        row["url"] = f"https://{host}/owner/repo/issues/1"
        self.responses["repos/owner/repo/issues?state=open&per_page=100"] = dict(data=[row])
        direct = dict(number=1, html_url="https://github.com/owner/repo/issues/1", state="open", title=row["title"], user=dict(login="author"), created_at=row["created_at"], updated_at="2026-09-27T00:00:00Z", labels=[], comments=0)
        self.responses["repos/owner/repo/issues/1"] = dict(data=direct)

        self.run_cli("fetch", "--host", host, "--include-item", "issue:1", ok=False)
        self.assertFalse((self.root / "data/owner/repo/raw/issues_and_prs.jsonl").exists())

        direct["html_url"] = row["url"]
        self.run_cli("fetch", "--host", host, "--include-item", "issue:1")
        self.run_cli("sync")
        self.assertEqual(self.ledger()[("issue", 1)]["url"], row["url"])
        self.assertEqual(json.loads((self.root / "data/owner/repo/raw/fetch_meta.json").read_text())["host"], host)
        self.assertTrue(all("--hostname" in call and call[call.index("--hostname") + 1] == host for call in self.calls()))

        calls = self.calls()
        self.run_cli("fetch", "--host", "github.com", ok=False)
        self.assertEqual(self.calls(), calls)
        self.assertEqual(self.ledger()[("issue", 1)]["url"], row["url"])

    def test_negative_duplicate_verdict_excludes_pair(self):
        self.sync()
        before = self.json_cli("similar", "--pairs")["pairs"]
        self.assertEqual(len(before), 1)
        self.json_cli("not-duplicate", "--key", "issue:1", "--key", "issue:2", "--by", "maintainer", "--note", "Different cause")
        self.assertEqual(self.json_cli("similar", "--pairs")["pairs"], [])
        self.assertEqual(len(self.json_cli("similar", "--pairs", "--include-checked")["pairs"]), 1)
        self.assertFalse((self.mock / "calls.jsonl").read_text().count("POST"))


class TrackingTests(Workspace):
    def test_budgeted_checks_rotate_starting_subscription(self):
        self.responses["repos/owner/repo"] = dict(data=repository())
        for number in (1, 2):
            self.responses[f"repos/owner/repo/issues/{number}"] = dict(data=summary("issue", number))
            self.responses[f"repos/owner/repo/issues/{number}/comments?per_page=100&page=1"] = dict(data=[])

        arguments = ("--expected-repo", "owner/repo")
        for number in (1, 2):
            self.json_cli("cache", *arguments, "track-add", "--kind", "issue", "--number", str(number), "--request-budget", "10")
            self.responses[f"repos/owner/repo/issues/{number}"]["data"]["comments"] = 1
            self.responses[f"repos/owner/repo/issues/{number}/comments?per_page=100&page=1"]["data"] = [dict(id=number, body="new", user=dict(login="author"), created_at="2026-09-23T00:00:00Z", updated_at="2026-09-23T00:00:00Z")]

        def check(budget):
            before = len(self.calls())
            result = self.json_cli("cache", *arguments, "track-check", "--request-budget", str(budget))
            calls = self.calls()[before:]
            summaries = [call[-1] for call in calls if call[-1].startswith("repos/owner/repo/issues/") and "?" not in call[-1]]
            return result, summaries

        first, summaries = check(4)
        self.assertEqual(summaries[0], "repos/owner/repo/issues/1")
        self.assertEqual(first["checked"], 1)

        second, summaries = check(5)
        self.assertEqual(summaries[0], "repos/owner/repo/issues/2")
        self.assertEqual(second["checked"], 1)
        self.assertEqual(second["unread_total"], 2)

        third, summaries = check(3)
        self.assertEqual(summaries[0], "repos/owner/repo/issues/1")
        self.assertEqual(third["checked"], 0)

        fourth, summaries = check(4)
        self.assertEqual(summaries[0], "repos/owner/repo/issues/2")
        self.assertEqual(fourth["unread_total"], 2)
        self.assertEqual([row["new_count"] for row in self.json_cli("cache", *arguments, "track-list")["rows"]], [1, 1])

    def test_repeated_enrollment_keeps_the_comment_baseline(self):
        self.seed_pr()
        arguments = ("--expected-repo", "owner/repo", "track-add", "--kind", "pr", "--number", "1", "--request-budget", "20")
        first = self.json_cli("cache", *arguments)
        before_calls = self.calls()
        before_record = (self.root / "data/owner/repo/local/tracked-items.json").read_bytes()

        second = self.json_cli("cache", *arguments)
        self.assertFalse(first["already_tracking"])
        self.assertTrue(second["already_tracking"])
        self.assertEqual(second["requests"], 0)
        self.assertEqual(self.calls(), before_calls)
        self.assertEqual((self.root / "data/owner/repo/local/tracked-items.json").read_bytes(), before_record)
