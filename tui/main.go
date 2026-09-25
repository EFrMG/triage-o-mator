// Command triage-o-mator is a terminal UI for browsing and triaging the issue/PR backlog managed by the bin/ scripts.
// It never writes data/<owner>/<repo>/ledger.jsonl directly: every mutation shells out to bin/apply; and every GitHub call it makes is read-only (fetch / sync / enrich-one).
// See tui/ghproc.go for the complete list of subprocesses it runs.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"
)

// installRootFromArgs is `--root <path>` (also `--root=<path>`), else the install found from the working directory.
// It is the only flag the TUI takes: everything else about a session comes from the install it runs in.
func installRootFromArgs() (string, error) {
	args := os.Args[1:]
	for i, arg := range args {
		value := ""
		switch {
		case arg == "--root" && i+1 < len(args):
			value = args[i+1]
		case len(arg) > len("--root=") && arg[:len("--root=")] == "--root=":
			value = arg[len("--root="):]
		default:
			continue
		}

		root, err := filepath.Abs(value)
		if err != nil {
			return "", err
		}

		if !isInstall(root) {
			return "", fmt.Errorf("%s is not a triage-o-mator install", root)
		}

		return root, nil
	}

	return FindInstallRoot()
}

// pickerModel is the session a bare checkout (or any directory with no install) opens with: Switch Repo over the installs bin/install-to has recorded, and nothing else until one is picked.
// The palettes come from the checkout this binary was built in, since there is no install to read them from.
func pickerModel() (model, error) {
	root, err := CodeRoot()
	if err != nil {
		return model{}, err
	}

	if err := loadTheme(root); err != nil {
		// go run . builds elsewhere, so fall back to a checkout around the working directory before giving up.
		if cwd, cwdErr := os.Getwd(); cwdErr == nil {
			for dir := cwd; ; {
				if loadTheme(dir) == nil {
					err = nil

					break
				}

				parent := filepath.Dir(dir)
				if parent == dir {
					break
				}

				dir = parent
			}
		}

		if err != nil {
			return model{}, err
		}
	}

	m := newModel("", "", Taxonomy{}, ReviewerName(), nil)
	m.codeRoot = root
	m.openRepoPicker()

	return m, nil
}

func run(m model) {
	m.actionHistoryLifecycle = &readLifecycle{}
	m.attentionLifecycle = &readLifecycle{}
	m.notificationsLifecycle = &readLifecycle{}
	m.evidenceLifecycle = &readLifecycle{}
	m.corpusLifecycle, m.corpusObserverLifecycle = &readLifecycle{}, &readLifecycle{}
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	_, err := p.Run()
	m.actionHistoryLifecycle.stop()
	m.attentionLifecycle.stop()
	m.notificationsLifecycle.stop()
	m.evidenceLifecycle.stop()
	m.corpusLifecycle.stop()
	m.corpusObserverLifecycle.stop()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func main() {
	installRoot, err := installRootFromArgs()
	if err != nil {
		picker, pickerErr := pickerModel()
		if pickerErr != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			fmt.Fprintln(os.Stderr, "error:", pickerErr)
			os.Exit(1)
		}

		run(picker)

		return
	}

	repo, err := ReadRepo(installRoot)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	taxonomy, err := LoadTaxonomy(installRoot)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	items, err := LoadLedger(installRoot, repo)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	if err := loadTheme(installRoot); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	m := newModel(installRoot, repo, taxonomy, ReviewerName(), items)
	m.codeRoot, _ = CodeRoot() // where bin/install-to lives, for installing into another repository from Switch Repo

	run(m)
}
