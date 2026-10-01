package main

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func composerHeader(title, previewTitle, target string, previewing bool, width int) string {
	color := currentTheme.Info
	if previewing {
		title, color = previewTitle, currentTheme.Success
	}

	heading := lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Bold(true).Render(title)
	label := ansi.Truncate(target, maxInt(width-ansi.StringWidth(heading)-1, 1), "…")
	gap := strings.Repeat(" ", maxInt(width-ansi.StringWidth(heading)-ansi.StringWidth(label), 1))
	return heading + gap + label
}

func (m model) composerPanel(header, content string) string {
	return panelStyle(true).BorderBackground(lipgloss.Color(currentTheme.Background)).Width(m.commentWidth()).Height(m.commentHeight()).Padding(0, 1).Render(header + "\n\n" + content)
}

func (m model) composerOverlay(background, panel string) string {
	x, y := m.commentPosition()
	base := screenStyle().Width(m.width).Height(m.mainHeight() + 2).Render(fitScreen(background, m.width, m.mainHeight()+2))
	return lipgloss.NewCompositor(lipgloss.NewLayer(base), lipgloss.NewLayer(panel).X(x).Y(y).Z(1)).Render()
}
