package main

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

// Only opt-in evidence/corpus operations use this lifecycle. Kill the process group, including gh, without deleting committed script checkpoints. Legacy reads and ledger writers are deliberately unaffected.
type readProcess struct {
	mu        sync.Mutex
	cmd       *exec.Cmd
	cancelled bool
}

type readLifecycle struct{ current *readProcess }

func (l *readLifecycle) stop() {
	if l != nil && l.current != nil {
		l.current.stop()
	}
}

func (p *readProcess) stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cancelled = true
	if p.cmd != nil {
		_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	}
}

func runReadScript(p *readProcess, root, name string, args ...string) (string, error) {
	cmd := exec.Command(filepath.Join(root, "bin", name), args...)
	cmd.Dir = root
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var out, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &stderr

	p.mu.Lock()
	if p.cancelled {
		p.mu.Unlock()
		return "", context.Canceled
	}
	err := cmd.Start()
	if err == nil {
		p.cmd = cmd
	}
	p.mu.Unlock()
	if err == nil {
		err = cmd.Wait()
	}

	p.mu.Lock()
	p.cmd = nil
	cancelled := p.cancelled
	p.mu.Unlock()
	if cancelled {
		return "", context.Canceled
	}
	if err != nil {
		return out.String(), &scriptError{script: name, args: args, output: strings.TrimSpace(stderr.String()), err: err}
	}
	return out.String(), nil
}
