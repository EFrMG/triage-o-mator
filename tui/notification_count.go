package main

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// track-list is sorted with unread items first and paged by its owning script. Read just enough pages to identify the unread items for an item count.
func unreadTrackedKeys(repo string, total int, read func(...string) (string, error)) ([]Key, error) {
	if total < 0 {
		return nil, fmt.Errorf("invalid unread tracked count")
	}
	var keys []Key
	seen := make(map[Key]bool)
	for offset := 0; len(keys) < total; {
		out, err := read("--expected-repo", repo, "track-list", "--offset", strconv.Itoa(offset), "--limit", "50")
		if err != nil {
			return nil, err
		}
		var page trackedPage
		if err := json.Unmarshal([]byte(out), &page); err != nil {
			return nil, err
		}
		if page.Repository.Name != repo || page.Offset != offset || page.UnreadTotal != total || len(page.Rows) == 0 || len(page.Rows) > 50 {
			return nil, fmt.Errorf("tracked notification count changed during paging")
		}
		for _, row := range page.Rows {
			if row.NewCount > 0 {
				key := row.key()
				if key.Number < 1 || key.Kind != "issue" && key.Kind != "pr" || seen[key] {
					return nil, fmt.Errorf("invalid or repeated tracked notification identity")
				}
				seen[key] = true
				keys = append(keys, key)
			}
		}
		if len(keys) >= total {
			break
		}
		if page.Next == nil || *page.Next <= offset || *page.Next != offset+len(page.Rows) {
			return nil, fmt.Errorf("tracked notification count changed during paging")
		}
		offset = *page.Next
	}
	if len(keys) != total {
		return nil, fmt.Errorf("tracked notification count changed during paging")
	}
	return keys, nil
}

func notificationCount(unreadTracked []Key, proposals actionProposalList) int {
	items := make(map[Key]bool, len(unreadTracked)+len(proposals.Rows))
	for _, key := range unreadTracked {
		items[key] = true
	}
	for _, proposal := range proposals.Rows {
		if proposal.Needs && proposal.Status != "executed" {
			items[Key{Kind: proposal.Kind, Number: proposal.Number}] = true
		}
	}
	return len(items)
}
