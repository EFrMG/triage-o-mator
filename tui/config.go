package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
)

// repoPattern is owner/repo as GitHub allows it, matching bin/_triage.py's REPO_RE; it is also what keeps data/<owner>/<repo>/ from resolving outside data/.
var repoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

func validRepo(repo string) bool {
	if !repoPattern.MatchString(repo) {
		return false
	}

	owner, name, _ := strings.Cut(repo, "/")

	return owner != "." && owner != ".." && name != "." && name != ".."
}

// DataDir is where one repo's ledger, raw fetch, batches, exports, and groups live, the same data/<owner>/<repo>/ folder bin/_triage.py uses.
func DataDir(installRoot, repo string) string {
	owner, name, _ := strings.Cut(repo, "/")

	return filepath.Join(installRoot, "data", owner, name)
}

// InstallDirName is the directory bin/install-to creates inside the repository being triaged, and MarkerName the file that marks it as an install; both match bin/_install.py.
const (
	InstallDirName = "triage-o-mator"
	MarkerName     = ".triage-install.json"
)

// FindInstallRoot locates the install this invocation belongs to: the TRIAGE_ROOT override, else the working directory, its triage-o-mator/ child, or the nearest of either walking up.
// It mirrors find_install() in bin/_install.py, so the TUI and the scripts it shells out to always agree on which install they are working in.
func FindInstallRoot() (string, error) {
	if override := os.Getenv("TRIAGE_ROOT"); override != "" {
		if !isInstall(override) {
			return "", fmt.Errorf("TRIAGE_ROOT=%s is not a triage-o-mator install", override)
		}

		return override, nil
	}

	start, err := os.Getwd()
	if err != nil {
		return "", err
	}

	for dir := start; ; {
		if isInstall(dir) {
			return dir, nil
		}

		if child := filepath.Join(dir, InstallDirName); isInstall(child) {
			return child, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no triage-o-mator install in %s or any parent directory — run bin/install-to /path/to/your/repository from the triage-o-mator checkout", start)
		}

		dir = parent
	}
}

// CodeRoot is the triage-o-mator checkout this binary was built in, reached through an install's symlinked bin/ when that is how it was started, which is why the path is resolved. It is where the themes live when there is no install to read them from.
func CodeRoot() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}

	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}

	return filepath.Dir(filepath.Dir(exe)), nil
}

func isInstall(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, MarkerName))

	return err == nil
}

func ReadRepo(installRoot string) (string, error) {
	data, err := os.ReadFile(filepath.Join(installRoot, "config", "repo"))
	if err != nil {
		return "", err
	}

	repo := strings.TrimSpace(string(data))
	if !validRepo(repo) {
		return "", fmt.Errorf("config/repo does not look like 'owner/repo': %q", repo)
	}

	return repo, nil
}

// WriteRepo overwrites config/repo, the same file bin/_triage.py's read_repo() reads.
// Each repo keeps its own data folder (see DataDir), so switching never mixes two repos' issue/PR numbers; callers reload their state from the new repo's folder afterwards.
func WriteRepo(installRoot, repo string) error {
	if !validRepo(repo) {
		return fmt.Errorf("%q doesn't look like 'owner/repo'", repo)
	}

	return os.WriteFile(filepath.Join(installRoot, "config", "repo"), []byte(repo+"\n"), 0o644)
}

// ReviewerName is the identity passed as --by to bin/apply, prefering git's configured user.name (never the account email) over the bare OS username.
func ReviewerName() string {
	if out, err := exec.Command("git", "config", "user.name").Output(); err == nil {
		if name := strings.TrimSpace(string(out)); name != "" {
			return name
		}
	}

	if u, err := user.Current(); err == nil && u.Username != "" {
		return u.Username
	}

	return "reviewer"
}
