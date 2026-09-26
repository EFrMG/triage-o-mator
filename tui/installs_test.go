package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// secondInstall is another repository's install, with its own taxonomy, config/repo and ledger, registered for this user as bin/install-to registers one.
func secondInstall(t *testing.T, repo string) string {
	t.Helper()
	root := batchFixture(t)
	if err := os.RemoveAll(DataDir(root, "owner/repo")); err != nil {
		t.Fatal(err)
	}

	if err := WriteRepo(root, repo); err != nil {
		t.Fatal(err)
	}

	writeLedger(t, root, repo, ledgerFixtureRow(7, "only in "+repo))

	return root
}

func registerInstalls(t *testing.T, roots ...string) {
	t.Helper()
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	var installs []map[string]string
	for _, root := range roots {
		installs = append(installs, map[string]string{"path": root, "repo": "", "updated_at": "2026-09-20T00:00:00Z"})
	}

	data, err := json.Marshal(map[string]any{"installs": installs})
	if err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(configHome, "triage-o-mator"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(configHome, "triage-o-mator", "installs.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func openSwitchRepo(m model) model {
	m.focus = FocusSidebar
	m.sidebar.selected = switchRepoIndex

	return press(m, "enter")
}

func TestSwitchRepoListsEveryRegisteredInstall(t *testing.T) {
	root := batchFixture(t)
	other := secondInstall(t, "other/backlog")
	registerInstalls(t, root, other)

	m := openSwitchRepo(batchModel(t, root))
	var found *repoInfo
	for i, info := range m.repoRecent {
		if info.name == "other/backlog" {
			found = &m.repoRecent[i]
		}
	}

	if found == nil || found.root != other {
		t.Fatalf("another install's repos should be listed with it: %+v", m.repoRecent)
	}

	if m.repoRecent[0].root != root {
		t.Fatalf("this install's repos come first: %+v", m.repoRecent)
	}

	if !strings.Contains(m.viewContent(), "other/backlog") {
		t.Fatal("a repo from elsewhere should be listed in the picker")
	}
}

func TestPickingARepoFromAnotherInstallMovesTheSessionThere(t *testing.T) {
	root := batchFixture(t)
	other := secondInstall(t, "other/backlog")
	registerInstalls(t, root, other)

	m := openSwitchRepo(batchModel(t, root))
	for m.repoPick < 0 || m.repoRecent[m.repoPick].name != "other/backlog" {
		m = press(m, "down")
	}

	m = press(m, "enter")
	if m.installRoot != other || m.repo != "other/backlog" {
		t.Fatalf("the session should move to that install: %s %s (%q)", m.installRoot, m.repo, m.status)
	}

	if got, err := ReadRepo(other); err != nil || got != "other/backlog" {
		t.Fatalf("the install's own config/repo is what its scripts read: %q %v", got, err)
	}

	if len(m.items) != 1 || m.items[0].Number != 7 {
		t.Fatalf("its ledger should be the one loaded: %+v", m.items)
	}
}

func TestATypedPathOpensAnInstallDirectlyOrSaysWhyNot(t *testing.T) {
	root := batchFixture(t)
	other := secondInstall(t, "other/backlog")
	registerInstalls(t, root)

	m := typeRepo(batchModel(t, root), other)
	if m.installRoot != other || m.repo != "other/backlog" {
		t.Fatalf("an absolute path to an install should open it: %s %s (%q)", m.installRoot, m.repo, m.status)
	}

	// The path of the repository that holds an install works too, since that is the path anyone would have at hand.
	repoDir := t.TempDir()
	inside := filepath.Join(repoDir, InstallDirName)
	if err := os.Rename(secondInstall(t, "third/repo"), inside); err != nil {
		t.Fatal(err)
	}

	m = typeRepo(batchModel(t, root), repoDir)
	if m.installRoot != inside || m.repo != "third/repo" {
		t.Fatalf("a repository holding an install should open it: %s %s (%q)", m.installRoot, m.repo, m.status)
	}

	plain := t.TempDir()
	m = typeRepo(batchModel(t, root), plain)
	if m.installRoot != root || m.installing.path != plain {
		t.Fatalf("a path with no install should offer to make one, not fail: %q %q", m.installing.path, m.status)
	}
}

// Started outside any install — in the triage-o-mator checkout, say — the TUI is only the picker, until it isn't.
func TestWithoutAnInstallTheAppIsOnlyThePicker(t *testing.T) {
	other := secondInstall(t, "other/backlog")
	registerInstalls(t, other)

	m := newModel("", "", Taxonomy{}, "tester", nil)
	m.openRepoPicker()
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 40})

	if !m.noInstall() || !m.editingRepo {
		t.Fatal("with no install, the picker is the whole app")
	}

	if m.Init() == nil {
		t.Fatal("the status tick should still run")
	}

	view := m.viewContent()
	if !strings.Contains(view, "no install open") || !strings.Contains(view, "before anything is written") {
		t.Fatalf("the picker should say what this screen is and that a path can be installed into:\n%s", view)
	}

	if !strings.Contains(view, "other/backlog") {
		t.Fatal("the installs recorded on this machine should be listed")
	}

	for m.repoPick < 0 || m.repoRecent[m.repoPick].name != "other/backlog" {
		m = press(m, "down")
	}

	m = press(m, "enter")
	if m.noInstall() || m.installRoot != other || m.repo != "other/backlog" {
		t.Fatalf("picking one should open it: %q %q (%q)", m.installRoot, m.repo, m.status)
	}

	if len(m.items) != 1 || m.items[0].Number != 7 {
		t.Fatalf("with its ledger loaded: %+v", m.items)
	}
}

func TestWithoutAnInstallEscapeQuitsAndARepoNameHasNowhereToGo(t *testing.T) {
	registerInstalls(t)
	m := newModel("", "", Taxonomy{}, "tester", nil)
	m.openRepoPicker()
	m = send(m, tea.WindowSizeMsg{Width: 120, Height: 40})

	m.repoInput.SetValue("acme/widgets")
	m = press(m, "enter")
	if m.installRoot != "" || !strings.Contains(m.status, "no install") && !strings.Contains(m.status, "No install") {
		t.Fatalf("a repo name needs an install to live in: %q", m.status)
	}

	next, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	if cmd == nil {
		t.Fatal("Esc should quit when there is nothing behind the picker")
	}

	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("Esc should quit, got %T", cmd())
	}

	_ = next
}

// How the TUI decides which install it is in, which is what every script it shells out to then works on.
func TestFindingTheInstallFromWhereYouAre(t *testing.T) {
	root := batchFixture(t)
	repoDir := t.TempDir()
	inside := filepath.Join(repoDir, InstallDirName)
	if err := os.Rename(root, inside); err != nil {
		t.Fatal(err)
	}

	deep := filepath.Join(repoDir, "src", "pkg")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, from := range []string{inside, repoDir, deep} {
		t.Chdir(from)
		got, err := FindInstallRoot()
		if err != nil || got != inside {
			t.Fatalf("from %s: got %q, %v; want the install", from, got, err)
		}
	}

	t.Chdir(t.TempDir())
	if _, err := FindInstallRoot(); err == nil || !strings.Contains(err.Error(), "bin/install-to") {
		t.Fatalf("outside any install the error should say how to make one: %v", err)
	}

	t.Setenv("TRIAGE_ROOT", inside)
	if got, err := FindInstallRoot(); err != nil || got != inside {
		t.Fatalf("TRIAGE_ROOT should win wherever you are: %q, %v", got, err)
	}

	t.Setenv("TRIAGE_ROOT", repoDir)
	if _, err := FindInstallRoot(); err == nil {
		t.Fatal("TRIAGE_ROOT pointing at something that is not an install should say so, not fall back")
	}
}

func TestTheRootFlagPicksTheInstall(t *testing.T) {
	root := batchFixture(t)
	t.Chdir(t.TempDir()) // nowhere near an install, so only the flag can find one

	for _, args := range [][]string{{"triage-o-mator", "--root", root}, {"triage-o-mator", "--root=" + root}} {
		os.Args = args
		got, err := installRootFromArgs()
		if err != nil || got != root {
			t.Fatalf("%v: got %q, %v", args, got, err)
		}
	}

	os.Args = []string{"triage-o-mator", "--root", t.TempDir()}
	if _, err := installRootFromArgs(); err == nil || !strings.Contains(err.Error(), "not a triage-o-mator install") {
		t.Fatalf("--root at a directory with no install should say so: %v", err)
	}
}

// flatten drops the whitespace a panel's wrapping introduces, so a test can look for text the view broke across lines.
func flatten(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\u2026", "")), "")
}

// gitRepo is a repository with nothing in it but a remote, the state someone is in when they first want the tool in it.
func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "."}, {"remote", "add", "origin", "git@github.com:acme/widgets.git"}} {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}

	return dir
}

// installerModel is a session whose bin/ scripts are this checkout's, which is how the app reaches bin/install-to before any install exists.
func installerModel(t *testing.T, root string) model {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	m := batchModel(t, root)
	m.codeRoot = checkoutRootForTest(t)

	return m
}

func TestTheInstallPlanIsShownBeforeAnythingIsWritten(t *testing.T) {
	target := gitRepo(t)
	m := installerModel(t, batchFixture(t))
	m = typeRepo(m, target) // Enter on a path with no install asks the script what it would change

	if m.installing.path != target || !m.installing.busy {
		t.Fatalf("Enter on such a path should start planning: %+v", m.installing)
	}

	m = runCmd(m, installPlanCmd(m.installerRoot(), target, false))
	if m.installing.busy || m.installing.plan == "" {
		t.Fatalf("the plan should arrive and be kept: %+v", m.installing)
	}

	flat := flatten(m.viewContent())
	for _, want := range []string{"acme/widgets", "create triage-o-mator/", "Nothing has been written yet", "Mode: tracked"} {
		if !strings.Contains(flat, flatten(want)) {
			t.Fatalf("the plan screen should show what the script said it would do, and what accepting it means (%q):\n%s", want, m.viewContent())
		}
	}

	// The rest of the plan is a scroll away, never silently cut. How many presses that takes depends on how far the plan's absolute paths wrap, so scroll until the view stops moving rather than a fixed count.
	settled := false
	for i := 0; i < 500 && !settled; i++ {
		before := m.viewContent()
		m = press(m, "j")
		settled = m.viewContent() == before
	}

	if !settled {
		t.Fatalf("scrolling never reached the end of the plan:\n%s", m.viewContent())
	}

	if !strings.Contains(flatten(m.viewContent()), flatten("triage-o-mator/config/repo")) {
		t.Fatalf("scrolling should reach the end of the plan:\n%s", m.viewContent())
	}

	if !strings.Contains(flatten(m.viewContent()), flatten("Nothing has been written yet")) {
		t.Fatalf("and the question stays on screen while it does:\n%s", m.viewContent())
	}

	if entries, err := os.ReadDir(target); err != nil || len(entries) != 1 {
		t.Fatalf("planning must not write anything: %v %v", entries, err)
	}
}

func TestTheOtherModeIsAPlanToo(t *testing.T) {
	target := gitRepo(t)
	m := installerModel(t, batchFixture(t))
	m = typeRepo(m, target)
	m = runCmd(m, installPlanCmd(m.installerRoot(), target, false))

	if !strings.Contains(flatten(m.viewContent()), flatten("committed there")) {
		t.Fatalf("the default plan is the tracked install:\n%s", m.viewContent())
	}

	m = press(m, "s")
	if !m.installing.solo || !m.installing.busy {
		t.Fatalf("s should ask for the other mode's plan: %+v", m.installing)
	}

	m = runCmd(m, installPlanCmd(m.installerRoot(), target, true))
	if !strings.Contains(flatten(m.viewContent()), flatten("kept out of that repository's history")) {
		t.Fatalf("and show it:\n%s", m.viewContent())
	}

	if entries, _ := os.ReadDir(target); len(entries) != 1 {
		t.Fatal("changing your mind must not write anything either")
	}
}

func TestAcceptingThePlanInstallsAndOpensIt(t *testing.T) {
	target := gitRepo(t)
	m := installerModel(t, batchFixture(t))
	m = typeRepo(m, target)
	m = runCmd(m, installPlanCmd(m.installerRoot(), target, false))

	m = press(m, "enter")
	if !m.installing.busy {
		t.Fatal("Enter on the plan should install")
	}

	m = runCmd(m, installCmd(m.installerRoot(), target, false))
	install := filepath.Join(target, InstallDirName)
	if m.installRoot != install || m.repo != "acme/widgets" {
		t.Fatalf("the session should open what it just made: %q %q (%q)", m.installRoot, m.repo, m.status)
	}

	if m.editingRepo || m.installing.path != "" {
		t.Fatal("and leave the picker")
	}

	if !isInstall(install) {
		t.Fatal("the install should be there")
	}
}

func TestEscapeLeavesTheRepositoryAsItWas(t *testing.T) {
	target := gitRepo(t)
	m := installerModel(t, batchFixture(t))
	m = typeRepo(m, target)
	m = runCmd(m, installPlanCmd(m.installerRoot(), target, false))

	m = press(m, "esc")
	if m.installing.path != "" || !strings.Contains(m.status, "Nothing was installed") {
		t.Fatalf("Esc should drop the plan: %+v %q", m.installing, m.status)
	}

	if entries, _ := os.ReadDir(target); len(entries) != 1 {
		t.Fatal("and write nothing")
	}
}

// The plan and the run must ask for the same thing, or the screen would be describing a different install than the one made.
func TestPlanAndRunAgreeOnEverythingButDryRun(t *testing.T) {
	plan := installArgs("/repo", false, true)
	run := installArgs("/repo", false, false)
	if len(plan) != len(run) || plan[len(plan)-1] != "--dry-run" || run[len(run)-1] != "--yes" {
		t.Fatalf("plan %v, run %v", plan, run)
	}

	for i := range plan[:len(plan)-1] {
		if plan[i] != run[i] {
			t.Fatalf("plan and run differ at %d: %v vs %v", i, plan, run)
		}
	}

	if solo := installArgs("/repo", true, true); solo[1] != "--solo" {
		t.Fatalf("solo should be asked for in the plan too: %v", solo)
	}
}
