package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Taxonomy mirrors config/taxonomy.json exactly.
type Taxonomy struct {
	IssueCategories []string `json:"issue_categories"`
	PRCategories    []string `json:"pr_categories"`
	Actions         []string `json:"actions"`
	Confidence      []string `json:"confidence"`
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
