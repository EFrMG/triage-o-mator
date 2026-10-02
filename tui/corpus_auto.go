package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// The preference is per repository and ignored with other machine-local state; it is UI configuration, not evidence.
func corpusAutoPath(root, repo string) string {
	return filepath.Join(DataDir(root, repo), "local", "dataset-auto.local")
}

func loadCorpusAuto(root, repo string) (bool, error) {
	if root == "" || !validRepo(repo) {
		return false, nil
	}

	data, err := os.ReadFile(corpusAutoPath(root, repo))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	switch string(data) {
	case "on\n":
		return true, nil
	case "off\n":
		return false, nil
	default:
		return false, fmt.Errorf("invalid automatic dataset preference")
	}
}

func saveCorpusAuto(root, repo string, enabled bool) error {
	if !validRepo(repo) {
		return fmt.Errorf("invalid repository for automatic dataset preference")
	}
	path := corpusAutoPath(root, repo)
	if info, err := os.Lstat(filepath.Dir(path)); err == nil && !info.IsDir() {
		return fmt.Errorf("invalid automatic dataset preference directory")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("invalid automatic dataset preference file")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	file, err := os.CreateTemp(filepath.Dir(path), ".dataset-auto-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	value := "off\n"
	if enabled {
		value = "on\n"
	}
	if _, err := file.WriteString(value); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

func autoProcessed(progress *corpusProgress) int {
	if progress == nil {
		return 0
	}
	return progress.Counts["complete"] + progress.Counts["gaps"] + progress.Counts["error"]
}

func autoResumeReason(reason string) bool {
	// Older selected datasets may still have an item-limit checkpoint from a previous TUI version.
	return reason == "item limit reached" || strings.HasPrefix(reason, "request budget exhausted")
}

func (m model) requestAutomaticCorpus() (tea.Model, tea.Cmd) {
	if !m.corpus.automatic || m.noInstall() || m.refreshing {
		return m, nil
	}
	if m.corpus.busy {
		m.corpus.autoQueued = true
		return m, nil
	}

	m.corpus.autoQueued = false
	m.corpus.autoRestore = true
	return m.startCorpus("restore")
}

func (m model) toggleAutomaticCorpus() (tea.Model, tea.Cmd) {
	enabled := !m.corpus.automatic
	if err := saveCorpusAuto(m.installRoot, m.repo, enabled); err != nil {
		m.failErr("Couldn't save automatic download preference", err)
		return m, nil
	}
	m.corpus.automatic = enabled
	m.corpus.preferenceProblem = ""
	if !enabled {
		m.corpus.autoQueued, m.corpus.autoRestore = false, false
		m.corpus.retryHard = false
		if m.corpus.busy && m.corpusLifecycle != nil {
			m.corpusLifecycle.stop()
			m.status = "Automatic download OFF; cancelling the current operation."
		} else {
			m.status = "Automatic download OFF."
		}
		return m, nil
	}
	m.status = "Automatic download ON."
	m.corpus.retryHard = true
	if m.corpus.busy {
		m.corpus.autoQueued = true
		return m, nil
	}
	if m.refreshing {
		return m, nil
	}
	return m.requestAutomaticCorpus()
}
