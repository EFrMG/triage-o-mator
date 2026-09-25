package main

import (
	"encoding/json"
	"fmt"
	"strconv"

	tea "github.com/charmbracelet/bubbletea"
)

const cacheMaxAgeArg = "86400"

const evidenceHost = "github.com"

type evidenceReadMsg struct {
	root                string
	repo                string
	key                 Key
	request, generation uint64
	data                EnrichedItem
	err                 error
}

func evidenceReadCmd(root, repo string, key Key, mode string, request, generation uint64, processes ...*readProcess) tea.Cmd {
	p := &readProcess{}
	if len(processes) != 0 {
		p = processes[0]
	}
	return func() tea.Msg {
		msg := evidenceReadMsg{root: root, repo: repo, key: key, request: request, generation: generation}
		if mode != "offline" && mode != "cache-preferred" && mode != "refresh" {
			msg.err = fmt.Errorf("unsupported evidence mode %q", mode)
			return msg
		}

		args := []string{"--kind", key.Kind, "--number", strconv.Itoa(key.Number), "--expected-repo", repo, "--cache-mode", mode, "--host", evidenceHost, "--max-age", cacheMaxAgeArg, "--request-budget", "100"}
		if key.Kind == "pr" {
			args = append(args, "--diff")
		}

		out, err := runReadScript(p, root, "enrich-one", args...)
		if err == nil {
			err = json.Unmarshal([]byte(out), &msg.data)
		}
		if err == nil && (msg.data.Evidence == nil || msg.data.Kind != key.Kind || msg.data.Number != key.Number || msg.data.Evidence.Mode != mode || (mode == "offline" && msg.data.Evidence.Stats.Requests != 0)) {
			err = fmt.Errorf("evidence response identity or mode mismatch")
		}

		msg.err = err
		msg.data.CachedRead, msg.data.DiffLoaded = true, key.Kind == "pr"
		return msg
	}
}

func (m model) finishEvidenceRead(msg evidenceReadMsg) (tea.Model, tea.Cmd) {
	if msg.root != m.installRoot || msg.request != m.evidenceRequest || msg.repo != m.repo {
		return m, nil
	}

	return m.finishNotificationPREvidence(msg)
}
