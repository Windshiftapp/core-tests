//go:build test

package cql

import "testing"

func TestLooksLikeQuery_StructuredFilters(t *testing.T) {
	// Queries that express an actual filter must be detected as CQL.
	cqlQueries := []string{
		`milestone = '0.8.2'`,
		`milestone = "0.8.2"`,
		`status != Done`,
		`milestone = '0.8.2' AND status != Done`,
		`status = open OR status = "in progress"`,
		`assignee = currentUser()`,
		`status IN (open, "in progress")`,
		`assignee IS NULL`,
		`assignee IS NOT NULL`,
		`NOT status = Done`,
		`title ~ bug`,
	}
	for _, q := range cqlQueries {
		t.Run(q, func(t *testing.T) {
			if !LooksLikeQuery(q) {
				t.Errorf("LooksLikeQuery(%q) = false, want true", q)
			}
		})
	}
}

func TestLooksLikeQuery_FreeText(t *testing.T) {
	// Free-text phrases — including single bare words that the grammar accepts
	// as a lone identifier/literal — must NOT be detected as CQL, so ordinary
	// searches fall through to full-text matching.
	textQueries := []string{
		``,
		`login`,
		`auth`,
		`login bug`,
		`rate limit`,
		`"rate limit"`,
		`fix the search endpoint`,
		`0.8.2`,
		`WI-516`,
		// "CONTAINS" is the word, not the `~` operator, so this is three bare
		// identifiers — not a filter.
		`labels CONTAINS bug`,
	}
	for _, q := range textQueries {
		t.Run(q, func(t *testing.T) {
			if LooksLikeQuery(q) {
				t.Errorf("LooksLikeQuery(%q) = true, want false", q)
			}
		})
	}
}
