"""bin/install-to, and what makes an install an install: scripts run through its symlinked bin/ work on the install's data and never on the checkout's, and refuse to run outside one.

Every test runs against a throwaway checkout and a throwaway git repository, so nothing here touches a real install, a real repo, or GitHub.
"""

import json
import os
import re
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


class InstallTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)

        # A copy of the checkout, so CODE_ROOT (and everything symlinked from it) stays inside the temporary directory.
        self.checkout = self.base / "checkout"
        shutil.copytree(ROOT / "bin", self.checkout / "bin", ignore=shutil.ignore_patterns("triage-o-mator", "__pycache__"))
        for name in ("config", "themes", "prompts", "docs"):
            shutil.copytree(ROOT / name, self.checkout / name)

        (self.checkout / "config" / "repo").unlink(missing_ok=True)
        for name in ("AGENTS.md", "README.md"):
            shutil.copyfile(ROOT / name, self.checkout / name)

        self.target = self.base / "target"
        self.target.mkdir()
        self.git("init", "-q", ".")
        self.git("remote", "add", "origin", "git@github.com:acme/widgets.git")
        (self.target / "README.md").write_text("# Widgets\n")

        self.install = self.target / "triage-o-mator"
        self.env = dict(os.environ, XDG_CONFIG_HOME=str(self.base / "config-home"))

    def git(self, *args, repo=None):
        result = subprocess.run(["git", "-C", str(repo or self.target), *args], capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)

        return result.stdout

    def install_to(self, *args, ok=True):
        result = subprocess.run([str(self.checkout / "bin" / "install-to"), str(self.target), *args], capture_output=True, text=True, env=self.env)
        if ok:
            self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        else:
            self.assertNotEqual(result.returncode, 0, result.stdout)

        return result.stdout + result.stderr

    def run_script(self, command, *args, cwd=None, ok=True):
        result = subprocess.run([str(self.install / "bin" / command), *args], cwd=str(cwd or self.install), capture_output=True, text=True, env=self.env)
        if ok:
            self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        else:
            self.assertNotEqual(result.returncode, 0, result.stdout)

        return result.stdout + result.stderr

    def tracked(self):
        """Everything git would commit in the target, which is the whole point of the generated .gitignore."""
        self.git("add", "-A")

        return sorted(line.split("\t")[-1] for line in self.git("diff", "--cached", "--name-only").splitlines())

    def ledger_row(self, number=1, title="One"):
        return json.dumps(dict(kind="issue", number=number, title=title, state="open", url=f"https://github.com/acme/widgets/issues/{number}", author="someone", created_at="2025-01-01T00:00:00Z", updated_at="", labels=[], comments_count=0, category="", action="", confidence="", reason="", reviewed=False)) + "\n"


class CheckoutHygieneTests(unittest.TestCase):
    """The checkout is the program, never a workspace: no config/repo, no ledger, no reports. That is what keeps a fork's pull request free of triage data belonging to whatever else its owner triages."""

    def test_the_checkout_keeps_no_triage_data_of_its_own(self):
        tracked = subprocess.run(["git", "ls-files", "data", "reports", "config/repo", "config/theme.local"], cwd=str(ROOT), capture_output=True, text=True).stdout.split()
        self.assertEqual(tracked, [], f"tracked here but belongs to an install: {tracked}")


class InstallToTests(InstallTest):
    def test_a_fresh_install_wires_the_checkout_and_tracks_only_the_repo_s_own_files(self):
        out = self.install_to("--yes")
        self.assertIn("repository to triage: acme/widgets", out, "the repo comes from the target's origin remote")

        for name in ("bin", "themes", "docs", "AGENTS.md", "README.md"):
            self.assertTrue((self.install / name).is_symlink(), f"{name} should be wired to the checkout, not copied")
        self.assertTrue((self.install / "prompts" / "auto-triage.md").is_symlink())
        self.assertFalse((self.install / "config" / "taxonomy.json").is_symlink(), "the taxonomy is the target's own, not the checkout's")
        self.assertEqual((self.install / "config" / "repo").read_text().strip(), "acme/widgets")

        # Every agentic tool looks for AGENTS.md; Claude Code looks for CLAUDE.md. Both lead to the one playbook.
        self.assertEqual(os.readlink(self.install / "AGENTS.md"), str(self.checkout / "prompts" / "PLAYBOOK.md"))
        self.assertEqual((self.install / "CLAUDE.md").read_text().strip(), "@AGENTS.md")
        self.assertIn("Triage playbook", (self.install / "AGENTS.md").read_text())

        marker = json.loads((self.install / ".triage-install.json").read_text())
        self.assertEqual((marker["repo"], marker["mode"], marker["tool"]), ("acme/widgets", "tracked", str(self.checkout)))

        self.assertEqual(
            self.tracked(),
            [
                "AGENTS.md",
                "README.md",
                "triage-o-mator/.gitignore",
                "triage-o-mator/CLAUDE.md",
                "triage-o-mator/config/repo",
                "triage-o-mator/config/taxonomy.json",
                "triage-o-mator/config/taxonomy.md",
            ],
            "symlinks and the machine-local marker must never be committed",
        )

    def test_a_fork_triages_its_upstream_repository(self):
        self.git("remote", "set-url", "origin", "git@github.com:contributor/widgets.git")
        self.git("remote", "add", "upstream", "https://github.com/acme/widgets.git")

        out = self.install_to("--solo", "--yes")

        self.assertIn("repository to triage: acme/widgets", out)
        self.assertEqual((self.install / "config" / "repo").read_text().strip(), "acme/widgets")
        self.assertTrue((self.install / "data" / "acme" / "widgets").is_dir())
        self.assertFalse((self.install / "data" / "contributor" / "widgets").exists())

    def test_running_it_again_repairs_symlinks_and_changes_nothing_else(self):
        self.install_to("--yes")
        before = self.tracked()

        moved = self.base / "moved-checkout"
        shutil.move(str(self.checkout), str(moved))
        self.checkout = moved

        out = self.install_to("--yes")
        self.assertIn("relink", out)
        self.assertEqual(os.readlink(self.install / "bin"), str(moved / "bin"))
        self.assertEqual(json.loads((self.install / ".triage-install.json").read_text())["tool"], str(moved))
        self.assertEqual(self.tracked(), before)

    def test_your_own_taxonomy_and_prompts_survive_a_re_run(self):
        self.install_to("--yes")
        (self.install / "prompts" / "auto-triage.md").unlink()
        (self.install / "prompts" / "auto-triage.md").write_text("# our own playbook\n")
        (self.install / "prompts" / "ours.md").write_text("# a new one\n")
        (self.install / "config" / "taxonomy.md").write_text("# our taxonomy\n")

        out = self.install_to("--yes")
        self.assertIn("keep", out)
        self.assertEqual((self.install / "prompts" / "auto-triage.md").read_text(), "# our own playbook\n")
        self.assertEqual((self.install / "config" / "taxonomy.md").read_text(), "# our taxonomy\n")
        self.assertTrue((self.install / "config" / "taxonomy.md.dist").exists(), "the checkout's version is kept to diff against")

        tracked = self.tracked()
        self.assertIn("triage-o-mator/prompts/auto-triage.md", tracked, "a prompt you wrote is yours, so it is committed")
        self.assertIn("triage-o-mator/prompts/ours.md", tracked)
        self.assertNotIn("triage-o-mator/prompts/review-pr.md", tracked, "one still symlinked is not")
        self.assertNotIn("triage-o-mator/config/taxonomy.md.dist", tracked)

    def test_solo_keeps_the_install_out_of_a_repository_you_do_not_control(self):
        self.install_to("--solo", "--yes")
        self.assertIn("/triage-o-mator/", (self.target / ".git" / "info" / "exclude").read_text())
        self.assertFalse((self.target / "AGENTS.md").exists(), "solo mode never edits the target's own files")
        self.assertEqual(self.tracked(), ["README.md"], "the install is invisible to this repository")

        self.assertEqual(json.loads((self.install / ".triage-install.json").read_text())["mode"], "solo")
        self.install_to("--yes")
        self.assertEqual(json.loads((self.install / ".triage-install.json").read_text())["mode"], "solo", "a re-run keeps the mode it was installed with")

    def test_adopt_turns_a_solo_install_into_one_the_repository_keeps(self):
        self.install_to("--solo", "--yes")
        self.install_to("--adopt", "--yes")

        self.assertNotIn("/triage-o-mator/", (self.target / ".git" / "info" / "exclude").read_text())
        self.assertIn("## Issue and PR triage", (self.target / "AGENTS.md").read_text())
        staged = self.git("diff", "--cached", "--name-only").splitlines()
        self.assertIn("triage-o-mator/config/repo", staged, "adopting stages the install, ready to commit")
        self.assertIn("AGENTS.md", staged)

    def test_the_agents_block_is_replaced_not_repeated(self):
        (self.target / "AGENTS.md").write_text("# Widgets\n\nBuild with make.\n")
        self.install_to("--yes")
        first = (self.target / "AGENTS.md").read_text()
        self.install_to("--yes")
        again = (self.target / "AGENTS.md").read_text()

        self.assertEqual(first, again)
        self.assertEqual(again.count("<!-- triage-o-mator:begin -->"), 1)
        self.assertTrue(again.startswith("# Widgets\n\nBuild with make.\n"), "what was already there stays")

    def test_dry_run_writes_nothing(self):
        out = self.install_to("--dry-run")
        self.assertIn("Nothing was written", out)
        self.assertFalse(self.install.exists())
        self.assertFalse((self.target / "AGENTS.md").exists())
        self.assertEqual(self.tracked(), ["README.md"])

    def test_repo_overrides_the_remote_and_is_remembered(self):
        self.git("remote", "add", "upstream", "git@github.com:acme/upstream.git")
        self.install_to("--repo", "other/backlog", "--yes")
        self.assertEqual((self.install / "config" / "repo").read_text().strip(), "other/backlog")
        self.assertTrue((self.install / "data" / "other" / "backlog").is_dir())

        self.install_to("--yes")
        self.assertEqual((self.install / "config" / "repo").read_text().strip(), "other/backlog", "a re-run keeps what this install triages")

    def test_every_link_an_install_carries_resolves_inside_it(self):
        """These files are read through the install's symlinks, so a link that only works in the checkout is a dead end for whoever is triaging."""
        self.install_to("--yes")

        docs = ["AGENTS.md", "README.md"] + sorted(str(p.relative_to(self.install)) for p in self.install.glob("docs/*.md")) + sorted(str(p.relative_to(self.install)) for p in self.install.glob("prompts/*.md"))
        self.assertIn("prompts/auto-triage.md", docs, "sanity: an install carries the playbooks")

        missing = []
        for name in docs:
            # The playbook's paths are written for the install root, which is where it is read as AGENTS.md.
            base = self.install if name == "prompts/PLAYBOOK.md" else (self.install / name).parent
            for target in re.findall(r"(?<!!)\[[^\]]*\]\((?!https?:|mailto:)([^)#]+)", (self.install / name).read_text()):
                if target != "url" and not (base / target.split("#")[0]).exists():
                    missing.append(f"{name} -> {target}")

        self.assertEqual(missing, [], f"dangling from inside the install: {missing}")

    def test_the_checkout_installs_into_itself(self):
        """triage-o-mator triaging its own backlog: the install is nested in the checkout, and its bin/ symlinks back to the very code it runs."""
        subprocess.run(["git", "init", "-q", "."], cwd=str(self.checkout), check=True, capture_output=True)
        subprocess.run(["git", "remote", "add", "origin", "git@github.com:efrmg/triage-o-mator.git"], cwd=str(self.checkout), check=True, capture_output=True)

        result = subprocess.run([str(self.checkout / "bin" / "install-to"), str(self.checkout), "--yes"], capture_output=True, text=True, env=self.env)
        self.assertEqual(result.returncode, 0, result.stderr + result.stdout)
        self.assertIn("this is the triage-o-mator checkout", result.stdout)

        install = self.checkout / "triage-o-mator"
        self.assertEqual(os.readlink(install / "bin"), str(self.checkout / "bin"))
        self.assertEqual((install / "config" / "repo").read_text().strip(), "efrmg/triage-o-mator")
        # The checkout's AGENTS.md is about the tool, and the install's is the playbook, so the usual block is not self-referential here either.
        checkout_agents = (self.checkout / "AGENTS.md").read_text()
        self.assertIn("## Issue and PR triage", checkout_agents)
        self.assertIn("triage-o-mator/AGENTS.md", checkout_agents)
        self.assertIn("Triage playbook", (install / "AGENTS.md").read_text())

        (install / "data" / "efrmg" / "triage-o-mator").mkdir(parents=True, exist_ok=True)
        (install / "data" / "efrmg" / "triage-o-mator" / "ledger.jsonl").write_text(self.ledger_row())
        subprocess.run([str(install / "bin" / "export-csv")], cwd=str(install), check=True, capture_output=True, env=self.env)
        self.assertTrue(list((install / "data" / "efrmg" / "triage-o-mator" / "exports").glob("*.csv")))
        self.assertFalse((self.checkout / "data").exists(), "the checkout still isn't a workspace; its install is")

    def test_a_target_that_is_not_a_git_repository_is_refused(self):
        plain = self.base / "plain"
        plain.mkdir()
        result = subprocess.run([str(self.checkout / "bin" / "install-to"), str(plain)], capture_output=True, text=True, env=self.env)
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("not a git repository", result.stderr)

        notrepo = self.base / "not-a-repo"
        notrepo.mkdir()
        result = subprocess.run([str(self.checkout / "bin" / "install-to"), str(notrepo)], capture_output=True, text=True, env=self.env)
        self.assertNotEqual(result.returncode, 0, "a directory that is not a git repository has nowhere to keep triage")

    def test_the_install_is_registered_for_switch_repo(self):
        self.install_to("--yes")
        registry = json.loads((self.base / "config-home" / "triage-o-mator" / "installs.json").read_text())
        self.assertEqual([(entry["path"], entry["repo"]) for entry in registry["installs"]], [(str(self.install), "acme/widgets")])


class InstallLayoutTests(InstallTest):
    """What the split between WORK_ROOT and CODE_ROOT buys, exercised through a real install."""

    def setUp(self):
        super().setUp()
        self.install_to("--yes")
        (self.install / "data" / "acme" / "widgets").mkdir(parents=True, exist_ok=True)
        (self.install / "data" / "acme" / "widgets" / "ledger.jsonl").write_text(self.ledger_row())

    def test_scripts_through_symlinked_bin_work_on_the_install(self):
        """The reason bin/_install.py uses os.path.abspath and never Path.resolve: resolving the bin/ symlink would send every export and report back into the checkout."""
        self.run_script("export-csv")
        self.assertTrue(list((self.install / "data" / "acme" / "widgets" / "exports").glob("*.csv")), "the export belongs to the install")

        self.run_script("report")
        self.assertTrue(list((self.install / "reports" / "acme" / "widgets").glob("*.md")), "so does the report")

        self.assertFalse((self.checkout / "data").exists(), "nothing may be written into the checkout")
        self.assertFalse((self.checkout / "reports").exists())

    def test_scripts_run_from_the_target_root_find_their_install(self):
        self.run_script("next", cwd=self.target)

    def test_what_a_script_prints_can_be_pasted_where_it_was_run(self):
        """Most people run this from the root of the repository being triaged, so a suggestion has to work from there, not only from inside the install."""
        from_root = self.run_script("next", cwd=self.target)
        self.assertIn("triage-o-mator/bin/", from_root, f"a command suggested from the repository root needs the prefix:\n{from_root}")

        for line in from_root.splitlines():
            for word in line.split():
                if word.startswith("bin/") or word.startswith("prompts/"):
                    self.fail(f"unprefixed, so it would not run from the repository root: {line}")

        inside = self.run_script("next", cwd=self.install)
        self.assertNotIn("triage-o-mator/bin/", inside, f"and no prefix when it is already the working directory:\n{inside}")

        # The paths it names have to exist from where it was run, which is the whole point.
        suggested = [w.strip("()[],.;:") for line in from_root.splitlines() for w in line.split() if w.startswith("triage-o-mator/")]
        missing = [w for w in suggested if not (self.target / w.split("#")[0]).exists()]
        self.assertEqual(missing, [], f"suggested but not there from the repository root: {missing}")

    def test_a_directory_without_a_marker_is_not_an_install(self):
        (self.checkout / "config" / "repo").write_text("acme/widgets\n")
        result = subprocess.run([str(self.checkout / "bin" / "stats")], cwd=str(self.checkout), capture_output=True, text=True)
        self.assertNotEqual(result.returncode, 0, "a checkout is not a workspace")
        self.assertIn("not a triage-o-mator install", result.stderr)
        self.assertIn("bin/install-to", result.stderr)

    def test_triage_root_overrides_where_the_install_is(self):
        elsewhere = self.base / "elsewhere"
        shutil.copytree(self.install, elsewhere, symlinks=True)
        env = dict(self.env, TRIAGE_ROOT=str(elsewhere))
        result = subprocess.run([str(self.install / "bin" / "export-csv")], cwd=str(self.install), env=env, capture_output=True, text=True)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue(list((elsewhere / "data" / "acme" / "widgets" / "exports").glob("*.csv")))
        self.assertFalse((self.install / "data" / "acme" / "widgets" / "exports").exists())


if __name__ == "__main__":
    unittest.main()
