package main

import "fmt"

type inventoryResult struct {
	Snapshot   string `json:"snapshot_id"`
	Items      int    `json:"items"`
	Scope      string `json:"scope"`
	Status     string `json:"status"`
	Repository struct {
		Host string `json:"host"`
		Name string `json:"full_name"`
	} `json:"repository"`
}

func (r inventoryResult) validate(repo string) error {
	if r.Repository.Host != evidenceHost || r.Repository.Name != repo || r.Scope != "full" || !((r.Status == "empty" && r.Items == 0 && r.Snapshot == "") || (r.Status == "imported" && r.Items > 0 && validCorpusID(r.Snapshot))) {
		return fmt.Errorf("inventory response identity or outcome mismatch")
	}

	return nil
}
