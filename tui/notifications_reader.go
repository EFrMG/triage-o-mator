package main

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
)

type notificationReaderCard struct {
	label, summary string
	mark           cardMark
}

// The reader presents one stable card per saved record, including its bounded excerpt.
func (m model) notificationReaderView(section, subtitle, note string, rows []notificationReaderCard, selected, scroll int, page attentionWindow) string {
	w := m.menuWidth()
	header := inset(titleBar("Notifications", subtitle, w)) + "\n"
	vp, _ := m.notificationReaderViewport(section, note, rows, selected, scroll, page)
	return header + vp.View()
}

func (m model) notificationReaderViewport(section, note string, rows []notificationReaderCard, selected, scroll int, page attentionWindow) (viewport.Model, int) {
	w := m.menuWidth()
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", inset(mutedText(wrapText(note, w-2))))
	fmt.Fprintf(&b, "%s\n", inset(mutedText(fmt.Sprintf("%s · %d–%d of %d retained", section, page.Offset+boolInt(page.Returned > 0), page.Offset+page.Returned, page.Total))))
	if len(rows) == 0 {
		fmt.Fprintf(&b, "\n%s", inset("No retained records in this section."))
	}
	fmt.Fprintln(&b)
	cardStart := strings.Count(b.String(), "\n")
	for i, row := range rows {
		fmt.Fprintf(&b, "%s\n", markedCard(row.label, row.summary, row.mark, i == selected, m.cardWidth()))
	}
	vp := viewport.New(viewport.WithWidth(m.cardWidth()), viewport.WithHeight(maxInt(m.mainHeight()-1, 1)))
	vp.SetContent(b.String())
	vp.SetYOffset(maxInt(0, cardStart+selected*cardHeight-vp.Height()/2))
	base := vp.YOffset()
	vp.SetYOffset(base + maxInt(0, scroll))
	return vp, base
}

func (m model) notificationReaderScroll(section, note string, rows []notificationReaderCard, selected, scroll int, page attentionWindow) int {
	vp, base := m.notificationReaderViewport(section, note, rows, selected, scroll, page)
	return vp.YOffset() - base
}

func firstReaderLine(value string) string {
	return strings.SplitN(strings.TrimSpace(value), "\n", 2)[0]
}
