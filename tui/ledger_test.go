package main

import (
	"path/filepath"
	"runtime"
	"testing"
)

// checkoutRootForTest is the triage-o-mator checkout (one level up from tui/), which still holds the taxonomy bin/install-to copies into every install.
func checkoutRootForTest(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not determine test file path")
	}

	return filepath.Dir(filepath.Dir(thisFile))
}

func TestTaxonomyLoadsWithEveryChoiceUsable(t *testing.T) {
	tax, err := LoadTaxonomy(checkoutRootForTest(t))
	if err != nil {
		t.Fatalf("LoadTaxonomy: %v", err)
	}

	// Counting the entries would test config/taxonomy.json, which is meant to grow; what the decision form and bin/apply depend on is that every choice in it is usable.
	for name, choices := range map[string][]string{"issue_categories": tax.IssueCategories, "pr_categories": tax.PRCategories, "actions": tax.Actions, "confidence": tax.Confidence} {
		if len(choices) == 0 {
			t.Errorf("%s is empty: the decision form would have nothing to offer", name)
		}

		seen := map[string]bool{}
		for _, choice := range choices {
			if choice == "" {
				t.Errorf("%s has a blank choice", name)
			}

			if seen[choice] {
				t.Errorf("%s repeats %q, so picking it is ambiguous", name, choice)
			}

			seen[choice] = true
		}
	}

	if tax.CategoriesFor("pr")[0] != tax.PRCategories[0] || tax.CategoriesFor("issue")[0] != tax.IssueCategories[0] {
		t.Error("CategoriesFor should give PRs the PR categories and everything else the issue ones")
	}
}
