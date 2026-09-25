package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func humanActionPreview(row actionHistoryRow) string {
	var entry struct {
		At    string `json:"at"`
		By    string `json:"by"`
		Claim struct {
			Rationale  string `json:"rationale"`
			Provenance string `json:"provenance"`
		} `json:"claim"`
	}
	if json.Unmarshal([]byte(row.Preview), &entry) == nil && entry.Claim.Rationale != "" {
		return sanitize(fmt.Sprintf("Explanation claimed: %s\nBy %s · %s · provenance %s", entry.Claim.Rationale, entry.By, entry.At, entry.Claim.Provenance))
	}
	// Bounded entry previews can end mid-JSON. The rationale appears near the beginning of the versioned entry; only present a complete quoted string.
	if at := strings.Index(row.Preview, `"rationale":`); at >= 0 {
		quoted := row.Preview[at+len(`"rationale":`):]
		if len(quoted) > 0 && quoted[0] == '"' {
			for i := 1; i < len(quoted); i++ {
				if quoted[i] == '"' && quoted[i-1] != '\\' {
					if value, err := strconv.Unquote(quoted[:i+1]); err == nil {
						return "Explanation claimed: " + sanitize(value) + "\nFull attributed entry and sources are in the offline cache."
					}
					break
				}
			}
		}
	}
	return "Attributed explanation saved; full record is available through the offline cache."
}

func humanAttentionPreview(section string, row attentionRow) string {
	if section == "list" {
		return notificationAttentionSummary(row) + "\nOpen for the retained discussion."
	}
	if detail, ok := parseAttentionHistory(row); ok {
		kind := attentionHistoryKind(detail)
		readHint := "Full comment is available through the offline cache."
		if detail.CommentExcerpt != "" {
			excerpt := detail.CommentExcerpt
			if detail.CommentExcerptOmitted > 0 {
				excerpt += "…"
			}
			author := "unknown contributor"
			if detail.CommentAuthor != "" {
				author = "@" + detail.CommentAuthor
			}
			return sanitize(fmt.Sprintf("%s from %s\n%s\n%s", kind, author, excerpt, readHint))
		}
		return kind + ". Full source is available through the offline cache."
	}
	return "Saved discussion entry; full source is available through the offline cache."
}

type attentionHistoryDetail struct {
	Relation                 string `json:"relation"`
	SourceID                 string `json:"source_id"`
	SuppliedClosureReference bool   `json:"supplied_closure_reference"`
	ReferenceCount           int    `json:"reference_count"`
	CommentAuthor            string `json:"comment_author"`
	CommentExcerpt           string `json:"comment_excerpt"`
	CommentExcerptOmitted    int    `json:"comment_excerpt_omitted_bytes"`
	SourceTimes              struct {
		CreatedAt   string `json:"created_at"`
		SubmittedAt string `json:"submitted_at"`
	} `json:"source_times"`
}

func parseAttentionHistory(row attentionRow) (attentionHistoryDetail, bool) {
	var detail attentionHistoryDetail
	return detail, json.Unmarshal([]byte(row.Preview), &detail) == nil
}

func attentionHistoryKind(detail attentionHistoryDetail) string {
	if detail.SuppliedClosureReference {
		return "Closure note"
	}
	if detail.Relation == "before-closure" {
		return "Earlier comment"
	}
	if strings.HasPrefix(detail.SourceID, "comment:") {
		return "Response after closure"
	}
	return "Observed event"
}

func attentionHistoryLabel(row attentionRow) string {
	detail, ok := parseAttentionHistory(row)
	if !ok {
		return "Saved discussion entry"
	}
	when := detail.SourceTimes.CreatedAt
	if when == "" {
		when = detail.SourceTimes.SubmittedAt
	}
	if len(when) >= 10 {
		when = when[:10]
	}
	if when == "" {
		when = "date unknown"
	}
	label := attentionHistoryKind(detail) + " · " + when
	if detail.CommentAuthor != "" {
		label += " · @" + detail.CommentAuthor
	}
	return sanitize(label)
}

func attentionHistorySummary(row attentionRow) string {
	detail, ok := parseAttentionHistory(row)
	if ok && detail.CommentExcerpt != "" {
		summary := strings.ReplaceAll(detail.CommentExcerpt, "\n", " ")
		if detail.CommentExcerptOmitted > 0 {
			summary += "…"
		}
		return sanitize(summary)
	}
	return firstReaderLine(humanAttentionPreview("history", row))
}
