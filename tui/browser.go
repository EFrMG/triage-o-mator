package main

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// openerGrace is how long the opener gets to fail. Openers like xdg-open usually hand off and exit at once, but some keep running as the browser itself; one still running after this long is taken to have worked.
const openerGrace = 3 * time.Second

type openedMsg struct {
	url string
	err error
}

// openerCommand builds the command that opens url in the OS default browser; tests replace it.
var openerCommand = func(url string) *exec.Cmd {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url)
	case "windows":
		return exec.Command("cmd", "/c", "start", url)
	}

	return exec.Command("xdg-open", url)
}

// openURLCmd opens url off the UI thread, and reports whether that actually worked: a missing opener, or one that exits with an error (no browser configured, say), comes back as err.
func openURLCmd(url string) tea.Cmd {
	return func() tea.Msg {
		cmd := openerCommand(url)

		var stderr strings.Builder
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			return openedMsg{url: url, err: err}
		}

		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			if err != nil {
				if detail := strings.TrimSpace(stderr.String()); detail != "" {
					err = fmt.Errorf("%s: %s", cmd.Path, singleLine(detail))
				}
			}

			return openedMsg{url: url, err: err}
		case <-time.After(openerGrace):
			return openedMsg{url: url}
		}
	}
}
