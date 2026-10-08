package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// Item mirrors one row of data/<owner>/<repo>/ledger.jsonl (bin/_triage.py's FIELDS).
// The TUI only reads ledger records; owning scripts such as bin/apply and bin/item-score write them.
type Item struct {
	Number          int            `json:"number"`
	Kind            string         `json:"kind"`
	State           string         `json:"state"`
	Title           string         `json:"title"`
	URL             string         `json:"url"`
	Author          string         `json:"author"`
	CreatedAt       string         `json:"created_at"`
	UpdatedAt       string         `json:"updated_at"`
	Labels          []string       `json:"labels"`
	ProposedLabels  []string       `json:"proposed_labels"`
	CommentsCount   int            `json:"comments_count"`
	HeadSHA         string         `json:"head_sha"` // PRs only, when a refresh read it for a scored PR
	ItemScore       *ItemScore     `json:"item_score"`
	Action          string         `json:"action"`
	Confidence      string         `json:"confidence"`
	Reason          string         `json:"reason"`
	TriagedAt       string         `json:"triaged_at"`
	TriagedBy       string         `json:"triaged_by"`
	BatchID         string         `json:"batch_id"`
	AgentNotes      string         `json:"agent_notes"`
	ReviewRequest   *ReviewRequest `json:"review_request"`
	MaintainerNotes string         `json:"maintainer_notes"`
	FirstSeenAt     string         `json:"first_seen_at"`
	LastSyncedAt    string         `json:"last_synced_at"`
}

type ItemScore struct {
	Rubric     string         `json:"rubric"`
	Value      *int           `json:"value"`
	Dimensions map[string]int `json:"dimensions"`
	Reason     string         `json:"reason"`
	Suggestion string         `json:"suggestion"`
	SnapshotID string         `json:"snapshot_id"`
	Revision   struct {
		UpdatedAt string `json:"updated_at"`
		HeadSHA   string `json:"head_sha"`
		Comments  *int   `json:"comments"`
	} `json:"revision"`
	AssessedAt string `json:"assessed_at"`
	AssessedBy string `json:"assessed_by"`
}

type ReviewRequest struct {
	By     string `json:"by"`
	At     string `json:"at"`
	Reason string `json:"reason"`
}

// Key uniquely identifies an item by (kind, number), matching bin/_triage.py's ledger_key().
type Key struct {
	Kind   string
	Number int
}

func (i Item) Key() Key { return Key{Kind: i.Kind, Number: i.Number} }

// scoreRubric, savedScore, scoreCurrent and ScoreValue mirror SCORE_RUBRIC, score_assessment_current and current_score in bin/_triage.py.
const scoreRubric = "item-quality-v1"

// savedScore is the recorded 0–5 number, whether or not the item has changed since.
func (i Item) savedScore() (int, bool) {
	if i.ItemScore == nil || i.ItemScore.Rubric != scoreRubric || i.ItemScore.Value == nil || *i.ItemScore.Value < 0 || *i.ItemScore.Value > 5 {
		return 0, false
	}

	return *i.ItemScore.Value, true
}

// scoreCurrent reports whether what the score examined is unchanged: a PR's head commit, an issue's comment count, else the recorded update time.
func (i Item) scoreCurrent() bool {
	revision := i.ItemScore.Revision
	if i.Kind == "pr" {
		if revision.HeadSHA != "" && i.HeadSHA != "" {
			return revision.HeadSHA == i.HeadSHA
		}
	} else if revision.Comments != nil {
		return *revision.Comments == i.CommentsCount
	}

	return revision.UpdatedAt != "" && revision.UpdatedAt == i.UpdatedAt
}

func (i Item) ScoreValue() (int, bool) {
	value, ok := i.savedScore()
	if !ok || !i.scoreCurrent() {
		return 0, false
	}

	return value, true
}

func (i Item) ScoreLabel() string {
	if value, ok := i.ScoreValue(); ok {
		return fmt.Sprintf("Score %d/5", value)
	}
	if value, ok := i.savedScore(); ok {
		return fmt.Sprintf("Score %d/5 (stale?)", value)
	}

	return "Score —"
}

func (i Item) Untriaged() bool {
	return len(i.ProposedLabels) == 0 && i.Action == ""
}

func (i Item) DecisionLabel() string {
	if len(i.ProposedLabels) > 0 {
		return strings.Join(i.ProposedLabels, ", ")
	}
	return "no labels"
}

// ByAgent reports a decision recorded by an agent: bin/apply credits agent proposals to "agent" or "agent:<contributor>" (AGENTS.md, rule 8).
func (i Item) ByAgent() bool {
	return i.TriagedBy == "agent" || strings.HasPrefix(i.TriagedBy, "agent:")
}

func (i Item) PendingReview() bool { return i.ReviewRequest != nil }

// readyLabel mirrors READY_LABEL in bin/_triage.py: on a PR the `ready` starter label means its code was read and nothing blocks merging.
const readyLabel = "ready"

func (i Item) MergeReadyHighConfidence() bool {
	return i.Kind == "pr" && slices.Contains(i.ProposedLabels, readyLabel) && i.Confidence == "high"
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
		if item.ReviewRequest != nil && (strings.TrimSpace(item.ReviewRequest.By) == "" ||
			strings.TrimSpace(item.ReviewRequest.At) == "" || strings.TrimSpace(item.ReviewRequest.Reason) == "") {
			return nil, fmt.Errorf("line %d: invalid Pending review request", lineNo)
		}

		items = append(items, item)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return items, nil
}
