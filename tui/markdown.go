package main

import (
	"fmt"
	"strings"
	"sync"

	chromastyles "github.com/alecthomas/chroma/v2/styles"
	"github.com/charmbracelet/glamour"
	gansi "github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/lucasb-eyer/go-colorful"
	"github.com/muesli/termenv"
)

// Item text (bodies, comments, agent notes) is Markdown and renders through glamour, styled from the active theme so it matches every palette in themes/. Diffs render as a fenced diff block, which glamour highlights through chroma with the same theme colors.

type rendererKey struct {
	theme string
	width int
}

var (
	renderersMu sync.Mutex
	renderers   = map[rendererKey]*glamour.TermRenderer{}
)

func strp(s string) *string { return &s }
func boolp(b bool) *bool    { return &b }
func uintp(u uint) *uint    { return &u }

// themeIsLight picks glamour's light base style for light palettes (Catppuccin Latte, Rosé Pine Dawn, ...).
func themeIsLight() bool {
	c, err := colorful.Hex(currentTheme.Background)
	if err != nil {
		return false
	}

	l, _, _ := c.Lab()

	return l > 0.6
}

// markdownStyle is glamour's dark or light style with the theme's colors, no document margin (the panels already pad), and headings without glamour's "##" prefixes.
func markdownStyle() gansi.StyleConfig {
	style := styles.DarkStyleConfig
	if themeIsLight() {
		style = styles.LightStyleConfig
	}

	t := currentTheme
	style.Document.Margin = uintp(0)
	style.Document.BlockPrefix, style.Document.BlockSuffix = "", ""
	style.Document.Color = strp(t.Foreground)
	style.Heading.Color, style.Heading.Bold = strp(t.Accent), boolp(true)
	for _, h := range []*gansi.StyleBlock{&style.H1, &style.H2, &style.H3, &style.H4, &style.H5, &style.H6} {
		h.Prefix, h.Suffix = "", ""
		h.Color, h.BackgroundColor = strp(t.Accent), nil
	}

	style.H1.Underline = boolp(true)
	style.Link.Color = strp(t.Accent)
	style.LinkText.Color = strp(t.Accent)
	style.BlockQuote.Color = strp(t.Muted)
	style.HorizontalRule.Color = strp(t.Border)
	style.Code.Color, style.Code.BackgroundColor = strp(t.Warning), strp(t.Selection)
	style.CodeBlock.Margin = uintp(0)
	style.CodeBlock.Theme = ""
	style.CodeBlock.Chroma = &gansi.Chroma{
		Text:              gansi.StylePrimitive{Color: strp(t.Foreground)},
		Comment:           gansi.StylePrimitive{Color: strp(t.Muted), Italic: boolp(true)},
		Keyword:           gansi.StylePrimitive{Color: strp(t.Accent)},
		KeywordType:       gansi.StylePrimitive{Color: strp(t.Accent)},
		NameFunction:      gansi.StylePrimitive{Color: strp(t.Accent)},
		NameBuiltin:       gansi.StylePrimitive{Color: strp(t.Accent)},
		LiteralString:     gansi.StylePrimitive{Color: strp(t.Success)},
		LiteralNumber:     gansi.StylePrimitive{Color: strp(t.Warning)},
		Operator:          gansi.StylePrimitive{Color: strp(t.Muted)},
		Punctuation:       gansi.StylePrimitive{Color: strp(t.Muted)},
		GenericInserted:   gansi.StylePrimitive{Color: strp(t.Success)},
		GenericDeleted:    gansi.StylePrimitive{Color: strp(t.Error)},
		GenericSubheading: gansi.StylePrimitive{Color: strp(t.Accent), Bold: boolp(true)},
		GenericStrong:     gansi.StylePrimitive{Bold: boolp(true)},
		GenericEmph:       gansi.StylePrimitive{Italic: boolp(true)},
		Error:             gansi.StylePrimitive{Color: strp(t.Error)},
	}

	return style
}

func markdownRenderer(width int) (*glamour.TermRenderer, error) {
	renderersMu.Lock()
	defer renderersMu.Unlock()

	k := rendererKey{theme: themeName, width: width}
	if r, ok := renderers[k]; ok {
		return r, nil
	}

	r, err := glamour.NewTermRenderer(glamour.WithStyles(markdownStyle()), glamour.WithWordWrap(width), glamour.WithColorProfile(lipgloss.ColorProfile()), glamour.WithChromaFormatter(chromaFormatter()))
	if err != nil {
		return nil, err
	}

	renderers[k] = r

	return r, nil
}

// chromaFormatter matches code block colors to the terminal's color profile. glamour always uses chroma's 256-color formatter otherwise, which rounds the theme's colors to palette entries the terminal may remap: on light themes that left diff context lines nearly invisible.
func chromaFormatter() string {
	switch lipgloss.ColorProfile() {
	case termenv.TrueColor:
		return "terminal16m"
	case termenv.ANSI:
		return "terminal16"
	default:
		return "terminal256"
	}
}

// forgetRenderers drops cached renderers after a theme change, so text re-renders in the new palette. glamour also registers its code block colors in chroma's global style registry, once, under a fixed name, and reuses them after that: remove them too, or diffs keep the first theme's colors.
func forgetRenderers() {
	renderersMu.Lock()
	renderers = map[rendererKey]*glamour.TermRenderer{}
	delete(chromastyles.Registry, glamourChromaStyle)
	renderersMu.Unlock()
}

// glamourChromaStyle is the name glamour registers its code block colors under (chromaStyleTheme in glamour/ansi).
const glamourChromaStyle = "charm"

// sanitize removes terminal escapes and control characters from text written by GitHub users before anything renders it.
func sanitize(text string) string {
	text = ansi.Strip(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\t", "    "))

	return strings.Map(func(r rune) rune {
		if r < 32 && r != '\n' {
			return -1
		}

		return r
	}, text)
}

// renderMarkdown renders sanitized Markdown to width columns, falling back to plain wrapped text if glamour fails. Every line is cut to width, since a long unbreakable token (a URL) would otherwise overflow the panel.
func renderMarkdown(src string, width int) string {
	width = maxInt(width, 10)
	clean := sanitize(src)
	r, err := markdownRenderer(width)
	if err != nil {
		return wrapText(clean, width)
	}

	out, err := r.Render(clean)
	if err != nil {
		return wrapText(clean, width)
	}

	lines := strings.Split(out, "\n")
	for i, line := range lines {
		lines[i] = ansi.Truncate(strings.TrimRight(line, " "), width, "")
	}

	// glamour pads blocks with blank lines (styled, so not just "\n"); drop them at both ends.
	blank := func(line string) bool { return strings.TrimSpace(ansi.Strip(line)) == "" }
	for len(lines) > 0 && blank(lines[0]) {
		lines = lines[1:]
	}

	for len(lines) > 0 && blank(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}

	return strings.Join(lines, "\n")
}

// renderDiff renders a PR diff as a highlighted diff block.
func renderDiff(diff string, width int) string {
	fence := "```"
	for strings.Contains(diff, fence) {
		fence += "`"
	}

	return renderMarkdown(fence+"diff\n"+strings.TrimRight(diff, "\n")+"\n"+fence, width)
}

// renderComments renders each comment on its own, under a colored separator naming its position.
func renderComments(comments []string, width int) string {
	if len(comments) == 0 {
		return mutedText("(no comments)")
	}

	parts := make([]string, len(comments))
	for i, c := range comments {
		label := fmt.Sprintf(" comment %d of %d ", i+1, len(comments))
		rule := strings.Repeat("─", maxInt(width-ansi.StringWidth(label)-2, 0))
		separator := lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Accent)).Render("──" + label + rule)
		parts[i] = separator + "\n" + renderMarkdown(c, width)
	}

	return strings.Join(parts, "\n\n")
}

func mutedText(s string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(currentTheme.Muted)).Render(s)
}
