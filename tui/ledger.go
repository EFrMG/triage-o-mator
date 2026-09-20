package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Item mirrors one row of data/<owner>/<repo>/ledger.jsonl (bin/_triage.py's FIELDS).
// Unknown/extra keys (e.g. state_reason) are preserved in Extra so nothing is lost if this struct is ever re-serialized (it currently is not, as the TUI only reads the ledger; bin/apply is the only writer).
type Item struct {
	Number        int      `json:"number"`
	Kind          string   `json:"kind"`
	State         string   `json:"state"`
	Title         string   `json:"title"`
	URL           string   `json:"url"`
	Author        string   `json:"author"`
	CreatedAt     string   `json:"created_at"`
	UpdatedAt     string   `json:"updated_at"`
	Labels        []string `json:"labels"`
	CommentsCount int      `json:"comments_count"`
	Category      string   `json:"category"`
	Action        string   `json:"action"`
	Confidence    string   `json:"confidence"`
	Reason        string   `json:"reason"`
	TriagedAt     string   `json:"triaged_at"`
	TriagedBy     string   `json:"triaged_by"`
	BatchID       string   `json:"batch_id"`
	AgentNotes    string   `json:"agent_notes"`
	Reviewed      bool     `json:"reviewed"`
	ReviewedBy    string   `json:"reviewed_by"`
	ReviewedAt    string   `json:"reviewed_at"`
	ReviewerNotes string   `json:"reviewer_notes"`
	FirstSeenAt   string   `json:"first_seen_at"`
	LastSyncedAt  string   `json:"last_synced_at"`
}

// Key uniquely identifies an item by (kind, number), matching bin/_triage.py's ledger_key().
type Key struct {
	Kind   string
	Number int
}

func (i Item) Key() Key { return Key{Kind: i.Kind, Number: i.Number} }

func (i Item) Untriaged() bool { return i.Category == "" }

// ByAgent reports a decision recorded by an agent: bin/apply credits agent proposals to "agent" or "agent:<contributor>" (AGENTS.md, rule 8).
func (i Item) ByAgent() bool {
	return i.TriagedBy == "agent" || strings.HasPrefix(i.TriagedBy, "agent:")
}

func (i Item) PendingReview() bool { return i.Category != "" && !i.Reviewed }

func (i Item) MergeReadyHighConfidence() bool {
	return i.Category == "merge-ready" && i.Confidence == "high" && !i.Reviewed
}

func (i Item) CloseCandidate() bool {
	switch i.Action {
	case "close-duplicate", "close-stale", "close-out-of-scope", "close-resolved":
		return !i.Reviewed
	}

	return false
}

// LoadLedger reads repo's ledger, data/<owner>/<repo>/ledger.jsonl.
// Missing file -> empty slice.
func LoadLedger(installRoot, repo string) ([]Item, error) {
	path := filepath.Join(DataDir(installRoot, repo), "ledger.jsonl")
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}

	defer f.Close()

	items, err := decodeJSONLItems(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}

	sort.Slice(items, func(a, b int) bool {
		if items[a].Kind != items[b].Kind {
			return items[a].Kind < items[b].Kind
		}

		return items[a].Number < items[b].Number
	})

	return items, nil
}

func decodeJSONLItems(f *os.File) ([]Item, error) {
	var items []Item
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		var item Item
		if err := json.Unmarshal(line, &item); err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNo, err)
		}

		items = append(items, item)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return items, nil
}
