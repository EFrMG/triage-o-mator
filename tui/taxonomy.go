package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Taxonomy mirrors config/taxonomy.json exactly.
type Taxonomy struct {
	IssueCategories []string          `json:"issue_categories"`
	PRCategories    []string          `json:"pr_categories"`
	Actions         []string          `json:"actions"`
	ActionGuidance  map[string]string `json:"action_guidance"`
	LabelCatalog    LabelCatalog      `json:"label_catalog"`
	Confidence      []string          `json:"confidence"`
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
