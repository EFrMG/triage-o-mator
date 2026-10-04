package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
)

const labelingViewLimit = 3

type labelingUI struct {
	loaded, enabled          bool
	tracked, pending, paused int
	pendingKeys, pausedKeys  []string
	policy                   *labelingPolicy
	preview                  *labelingPreview
	result                   string
	offset                   int
}

type labelingPolicy struct {
	Repository    string `json:"repository"`
	Operation     string `json:"operation"`
	Before        bool   `json:"before"`
	After         bool   `json:"after"`
	PreviewSHA256 string `json:"preview_sha256"`
}

type labelingPreview struct {
	Repository    string                `json:"repository"`
	Enabled       bool                  `json:"enabled"`
	Limit         int                   `json:"limit"`
	RequestBudget int                   `json:"request_budget"`
	Requests      int                   `json:"requests"`
	PreviewSHA256 string                `json:"preview_sha256"`
	Items         []labelingPreviewItem `json:"items"`
}

type labelingPreviewItem struct {
	Kind           string   `json:"kind"`
	Number         int      `json:"number"`
	Status         string   `json:"status"`
	Desired        []string `json:"desired"`
	ObservedLabels []string `json:"observed_labels"`
	Add            []string `json:"add"`
	Remove         []string `json:"remove"`
}

type labelingStatus struct {
	Repository string   `json:"repository"`
	Enabled    bool     `json:"enabled"`
	Tracked    int      `json:"tracked_items"`
	Pending    []string `json:"pending_items"`
	Paused     []string `json:"paused_items"`
}

type labelingResult struct {
	Outcomes []struct {
		Key    string `json:"key"`
		Status string `json:"status"`
		Reason string `json:"reason"`
	} `json:"outcomes"`
}

type labelingMsg struct {
	root, repo, operation string
	request               uint64
	status                labelingStatus
	policy                labelingPolicy
	preview               labelingPreview
	result                labelingResult
	err                   error
}

func labelingCmd(root, repo string, request uint64, operation, hash string, limit int) tea.Cmd {
	return func() tea.Msg {
		args := []string{"status", "--expected-repo", repo}
		switch operation {
		case "enable-preview", "disable-preview", "enable-apply", "disable-apply":
			name := "enable"
			if strings.HasPrefix(operation, "disable") {
				name = "disable"
			}
			args[0] = name
			if strings.HasSuffix(operation, "apply") {
				args = append(args, "--apply", "--preview-sha256", hash)
			}
		case "pass-preview":
			args = []string{"preview", "--expected-repo", repo, "--limit", fmt.Sprint(limit)}
		case "pass-run":
			args = []string{"run", "--expected-repo", repo, "--limit", fmt.Sprint(limit), "--preview-sha256", hash}
		}
		out, err := runScript(root, "item-labels", args...)
		msg := labelingMsg{root: root, repo: repo, request: request, operation: operation, err: err}
		if err == nil {
			switch operation {
			case "status", "enable-apply", "disable-apply":
				msg.err = json.Unmarshal([]byte(out), &msg.status)
			case "enable-preview", "disable-preview":
				msg.err = json.Unmarshal([]byte(out), &msg.policy)
			case "pass-preview":
				msg.err = json.Unmarshal([]byte(out), &msg.preview)
			case "pass-run":
				msg.err = json.Unmarshal([]byte(out), &msg.result)
			}
		}
		return msg
	}
}

func (m model) labelingContent() string {
	w := m.menuWidth()
	state := "Reading settings…"
	if m.settings.labeling.loaded {
		state = "Disabled"
		if m.settings.labeling.enabled {
			state = "Enabled"
		}
	}
	view := inset(titleBar("Item labeling", state, w)) + "\n\n"
	view += inset("Applies proposed labels in bounded passes after this repository is enabled. A preview lists exact changes; applying rechecks GitHub before each write.") + "\n"
	if policy := m.settings.labeling.policy; policy != nil {
		view += "\n" + inset(fmt.Sprintf("%s item labeling for %s? Press e again to confirm.", strings.ToUpper(policy.Operation[:1])+policy.Operation[1:], policy.Repository)) + "\n"
	}
	if m.settings.labeling.loaded {
		view += "\n" + inset(fmt.Sprintf("Tracked: %d · uncertain: %d · paused after correction: %d", m.settings.labeling.tracked, m.settings.labeling.pending, m.settings.labeling.paused)) + "\n"
		if len(m.settings.labeling.pendingKeys) > 0 {
			view += inset(wrapText("Uncertain items: "+strings.Join(m.settings.labeling.pendingKeys, ", "), maxInt(m.cardWidth()-1, 1))) + "\n"
		}
		if len(m.settings.labeling.pausedKeys) > 0 {
			view += inset(wrapText("Paused items: "+strings.Join(m.settings.labeling.pausedKeys, ", "), maxInt(m.cardWidth()-1, 1))) + "\n"
		}
	}
	if preview := m.settings.labeling.preview; preview != nil {
		view += "\n" + inset(fmt.Sprintf("Preview for %s · %d item(s) · %d GitHub read(s)", preview.Repository, len(preview.Items), preview.Requests)) + "\n"
		for _, item := range preview.Items {
			view += "\n" + inset(fmt.Sprintf("%s #%d · %s", item.Kind, item.Number, item.Status)) + "\n"
			current := "not read"
			if item.ObservedLabels != nil {
				current = labelNames(item.ObservedLabels)
			}
			for _, line := range []string{
				"Current GitHub labels: " + current,
				"Proposed labels: " + labelNames(item.Desired),
				"Add: " + labelNames(item.Add),
				"Remove: " + labelNames(item.Remove),
			} {
				view += inset(wrapText(line, maxInt(m.cardWidth()-1, 1))) + "\n"
			}
		}
	}
	if m.settings.labeling.result != "" {
		view += "\n" + inset(wrapText(m.settings.labeling.result, maxInt(m.cardWidth()-1, 1)))
	}
	return view
}

func labelNames(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

func (m model) labelingViewport() viewport.Model {
	vp := viewport.New(viewport.WithWidth(m.cardWidth()), viewport.WithHeight(m.mainHeight()))
	vp.SetContent(m.labelingContent())
	vp.SetYOffset(m.settings.labeling.offset)
	return vp
}

func (m model) labelingView() string { return m.labelingViewport().View() }

func (m model) handleLabelingKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc", "h", "left":
		m.settings.section = ""
		return m, nil
	case "q":
		return m.requestQuit()
	case "?":
		m.showHelp = !m.showHelp
	case "r":
		m.settings.busy = true
		m.settings.request++
		return m, labelingCmd(m.installRoot, m.repo, m.settings.request, "status", "", 0)
	case "j", "down", "ctrl+d":
		vp := m.labelingViewport()
		step := 1
		if msg.String() == "ctrl+d" {
			step = maxInt(m.mainHeight()/2, 1)
		}
		vp.SetYOffset(vp.YOffset() + step)
		m.settings.labeling.offset = vp.YOffset()
	case "k", "up", "ctrl+u":
		vp := m.labelingViewport()
		step := 1
		if msg.String() == "ctrl+u" {
			step = maxInt(m.mainHeight()/2, 1)
		}
		vp.SetYOffset(vp.YOffset() - step)
		m.settings.labeling.offset = vp.YOffset()
	case "e":
		operation, hash := "enable-preview", ""
		if m.settings.labeling.enabled {
			operation = "disable-preview"
		}
		if plan := m.settings.labeling.policy; plan != nil {
			operation, hash = plan.Operation+"-apply", plan.PreviewSHA256
		}
		m.settings.busy = true
		m.settings.request++
		return m, labelingCmd(m.installRoot, m.repo, m.settings.request, operation, hash, 0)
	case "p":
		m.settings.busy = true
		m.settings.request++
		m.settings.labeling.preview = nil
		return m, labelingCmd(m.installRoot, m.repo, m.settings.request, "pass-preview", "", labelingViewLimit)
	case "a":
		if m.settings.labeling.preview == nil || !m.settings.labeling.enabled {
			m.status = "Enable item labeling and preview a pass first."
			return m, nil
		}
		m.settings.busy = true
		m.settings.request++
		return m, labelingCmd(m.installRoot, m.repo, m.settings.request, "pass-run", m.settings.labeling.preview.PreviewSHA256, labelingViewLimit)
	}
	return m, nil
}

func (m model) finishLabeling(msg labelingMsg) (tea.Model, tea.Cmd) {
	if !m.settings.open || m.settings.section != "labeling" || msg.root != m.installRoot || msg.repo != m.repo || msg.request != m.settings.request {
		return m, nil
	}
	m.settings.busy = false
	if msg.err != nil {
		m.failErr("Couldn't update item labeling", msg.err)
		return m, nil
	}
	switch msg.operation {
	case "status", "enable-apply", "disable-apply":
		if msg.status.Repository != m.repo {
			m.fail("Item labeling status belongs to another repository.")
			return m, nil
		}
		m.settings.labeling.loaded = true
		m.settings.labeling.enabled = msg.status.Enabled
		m.settings.labeling.tracked = msg.status.Tracked
		m.settings.labeling.pending = len(msg.status.Pending)
		m.settings.labeling.paused = len(msg.status.Paused)
		m.settings.labeling.pendingKeys = msg.status.Pending
		m.settings.labeling.pausedKeys = msg.status.Paused
		m.settings.labeling.policy = nil
		m.settings.labeling.preview = nil
	case "enable-preview", "disable-preview":
		if msg.policy.Repository != m.repo || msg.policy.PreviewSHA256 == "" || (msg.policy.Operation != "enable" && msg.policy.Operation != "disable") {
			m.fail("Item labeling policy preview did not match this repository.")
			return m, nil
		}
		m.settings.labeling.policy = &msg.policy
	case "pass-preview":
		if msg.preview.Repository != m.repo || msg.preview.PreviewSHA256 == "" {
			m.fail("Item labeling pass preview did not match this repository.")
			return m, nil
		}
		m.settings.labeling.preview = &msg.preview
		m.settings.labeling.offset = 0
	case "pass-run":
		written := 0
		lines := []string{}
		for _, outcome := range msg.result.Outcomes {
			if outcome.Status == "written" {
				written++
			}
			line := outcome.Key + " · " + outcome.Status
			if outcome.Reason != "" {
				line += ": " + outcome.Reason
			}
			lines = append(lines, line)
		}
		m.settings.labeling.result = fmt.Sprintf("Pass finished: %d written, %d other outcomes. Refresh to inspect current state.", written, len(msg.result.Outcomes)-written)
		if len(lines) > 0 {
			m.settings.labeling.result += "\n" + strings.Join(lines, "\n")
		}
		m.settings.labeling.preview = nil
	}
	return m, nil
}
