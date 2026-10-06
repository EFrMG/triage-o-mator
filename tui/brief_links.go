package main

import (
	"bytes"
	"net/url"
	"strconv"
	"strings"

	gansi "charm.land/glamour/v2/ansi"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// briefItemLink accepts only Markdown links to a numbered issue or PR in the selected repository. Like the rest of the TUI it reads one host, evidenceHost.
func briefItemLink(destination []byte, repo string) (Key, bool) {
	raw := string(destination)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != evidenceHost || u.User != nil || strings.ContainsAny(raw, "?#") || u.RawPath != "" {
		return Key{}, false
	}

	parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	selected := strings.Split(repo, "/")
	if len(parts) != 4 || len(selected) != 2 || !strings.EqualFold(parts[0], selected[0]) || !strings.EqualFold(parts[1], selected[1]) {
		return Key{}, false
	}

	kind := ""
	switch parts[2] {
	case "issues":
		kind = "issue"
	case "pull":
		kind = "pr"
	default:
		return Key{}, false
	}

	number, err := strconv.Atoi(parts[3])
	if err != nil || number < 1 || strconv.Itoa(number) != parts[3] {
		return Key{}, false
	}

	return Key{Kind: kind, Number: number}, true
}

// renderBriefMarkdown keeps the saved Markdown intact while rendering local item links as their labels. The same parsed links supply the Briefs item cards in mention order; code, bare URLs and other repositories do not become cards.
func renderBriefMarkdown(src, repo string, width int) (string, []Key) {
	width = maxInt(width, 10)
	clean := []byte(sanitize(src))
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM, extension.DefinitionList),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()),
		goldmark.WithRenderer(renderer.NewRenderer(renderer.WithNodeRenderers(util.Prioritized(gansi.NewRenderer(gansi.Options{
			WordWrap: width, Styles: markdownStyle(), ChromaFormatter: chromaFormatter(),
		}), 1000)))),
	)
	doc := md.Parser().Parse(text.NewReader(clean))
	seen := map[Key]bool{}
	var keys []Key
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		link, ok := node.(*ast.Link)
		if !entering || !ok {
			return ast.WalkContinue, nil
		}

		key, ok := briefItemLink(link.Destination, repo)
		if !ok {
			return ast.WalkContinue, nil
		}

		if !seen[key] {
			keys = append(keys, key)
			seen[key] = true
		}
		link.Destination = []byte("#")
		return ast.WalkContinue, nil
	})

	var out bytes.Buffer
	if err := md.Renderer().Render(&out, clean, doc); err != nil {
		return renderMarkdown(src, width), keys
	}

	return finishMarkdownRender(out.String(), width), keys
}
