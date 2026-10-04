package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

var actionOperations = []string{"comment", "close", "reopen", "none"}

// Taxonomy mirrors config/taxonomy.json exactly.
type Taxonomy struct {
	IssueCategories  []string          `json:"issue_categories"`
	PRCategories     []string          `json:"pr_categories"`
	Actions          []string          `json:"actions"`
	ActionGuidance   map[string]string `json:"action_guidance"`
	ActionOperations map[string]string `json:"action_operations"`
	LabelCatalog     LabelCatalog      `json:"label_catalog"`
	Confidence       []string          `json:"confidence"`
}

type LabelCatalog struct {
	Repository string        `json:"repository"`
	Status     string        `json:"status"`
	ObservedAt string        `json:"observed_at"`
	Labels     []GitHubLabel `json:"labels"`
}

type GitHubLabel struct {
	ID          int      `json:"id"`
	Name        string   `json:"name"`
	Color       string   `json:"color"`
	Description string   `json:"description"`
	Guidance    string   `json:"guidance"`
	Previous    []string `json:"previous_names"`
}

// CategoriesFor returns the valid category list for the given item kind.
func (t Taxonomy) CategoriesFor(kind string) []string {
	if kind == "pr" {
		return t.PRCategories
	}

	return t.IssueCategories
}

func (t Taxonomy) OperationFor(action string) string {
	if operation, found := t.ActionOperations[action]; found {
		if slices.Contains(actionOperations, operation) {
			return operation
		}

		return ""
	}

	// Older installs own their taxonomy copy and have no action_operations map. These names already describe supported writes.
	switch action {
	case "comment-request-info":
		return "comment"
	case "comment-explain-close", "close-duplicate", "close-stale", "close-out-of-scope", "close-resolved":
		return "close"
	case "no-action-needed":
		return "none"
	}

	return ""
}

func (t Taxonomy) SelectableActions() []string {
	var actions []string
	for _, action := range t.Actions {
		if slices.Contains(actionOperations, t.OperationFor(action)) {
			actions = append(actions, action)
		}
	}

	return actions
}

func LoadTaxonomy(installRoot string) (Taxonomy, error) {
	path := filepath.Join(installRoot, "config", "taxonomy.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return Taxonomy{}, fmt.Errorf("reading %s: %w", path, err)
	}

	var t Taxonomy
	if err := json.Unmarshal(data, &t); err != nil {
		return Taxonomy{}, fmt.Errorf("parsing %s: %w", path, err)
	}

	return t, nil
}
