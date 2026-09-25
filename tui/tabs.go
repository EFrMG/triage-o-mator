package main

import "sort"

// Tab defines one sidebar entry: a name and how to derive its item list from the full ledger.
// It filters mirror bin/report's sections exactly so the TUI's counts and the markdown report always agree.
type Tab struct {
	Name   string
	Filter func([]Item) []Item
}

func byCreatedAtAsc(items []Item) []Item {
	out := append([]Item(nil), items...)
	sort.Slice(out, func(a, b int) bool { return out[a].CreatedAt < out[b].CreatedAt })

	return out
}

func byCreatedAtDesc(items []Item) []Item {
	out := append([]Item(nil), items...)
	sort.Slice(out, func(a, b int) bool { return out[a].CreatedAt > out[b].CreatedAt })

	return out
}

func openOnly(items []Item) []Item {
	var out []Item
	for _, it := range items {
		if it.State == "open" {
			out = append(out, it)
		}
	}

	return out
}

func filterKind(items []Item, kind string) []Item {
	var out []Item
	for _, it := range items {
		if it.Kind == kind {
			out = append(out, it)
		}
	}

	return out
}

func untriagedOpen(items []Item) []Item {
	var out []Item
	for _, it := range openOnly(items) {
		if it.Untriaged() {
			out = append(out, it)
		}
	}

	return byCreatedAtAsc(out)
}

const (
	untriagedTab = iota
	pendingReviewTab
	mergeReadyTab
	closeCandidatesTab
	allItemsTab
)

var tabs = []Tab{
	{Name: "Untriaged", Filter: untriagedOpen},
	{Name: "Pending Review", Filter: func(items []Item) []Item {
		var out []Item
		for _, it := range openOnly(items) {
			if it.PendingReview() {
				out = append(out, it)
			}
		}

		return byCreatedAtAsc(out)
	}},
	{Name: "Merge-Ready PRs", Filter: func(items []Item) []Item {
		var out []Item
		for _, it := range openOnly(items) {
			if it.MergeReadyHighConfidence() {
				out = append(out, it)
			}
		}

		return byCreatedAtAsc(out)
	}},
	{Name: "Close Candidates", Filter: func(items []Item) []Item {
		var out []Item
		for _, it := range openOnly(items) {
			if it.CloseCandidate() {
				out = append(out, it)
			}
		}

		return byCreatedAtAsc(out)
	}},
	{Name: "All Items", Filter: func(items []Item) []Item {
		return byCreatedAtAsc(items)
	}},
}

func (m *model) cycleUntriagedKind() {
	m.untriagedKind = (m.untriagedKind + 1) % 3
	m.updateUntriagedView()
}

func (m *model) toggleUntriagedOrder() {
	m.untriagedNewest = !m.untriagedNewest
	m.updateUntriagedView()
}

func (m *model) updateUntriagedView() {
	m.clearTicks()
	m.recomputeSidebarCounts()
	m.refreshActiveList()
	m.status = "Showing " + m.tabName(untriagedTab) + "."
}

// The non-tab sidebar rows follow the real tabs: Batches, Groups, Possible Duplicates, Notifications, and Switch Repo.
// Overview isn't a tab at all: it is what the main area shows by default before any tab is entered (see model.View).
var (
	batchesIndex       = len(tabs)
	groupsIndex        = len(tabs) + 1
	pairsIndex         = len(tabs) + 2
	notificationsIndex = len(tabs) + 3
	switchRepoIndex    = len(tabs) + 4
)
