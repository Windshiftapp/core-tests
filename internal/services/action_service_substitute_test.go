package services

import (
	"testing"

	"windshift/internal/models"
)

// TestSubstituteVariables_SCMNamespace pins the variable substitution
// contract the create_milestone action depends on: keys like "ref.short"
// arrive on the event in NewValues, get re-keyed to "new_ref.short" by
// the event init code, and must resolve when templates reference them as
// {{ref.short}}. Mirrors the same shape for repo.* and commits.*.
//
// Uses a zero-value ActionService because substituteVariables only reads
// from the passed ExecutionContext; no service-level deps are touched.
func TestSubstituteVariables_SCMNamespace(t *testing.T) {
	as := &ActionService{}
	ctx := &models.ExecutionContext{Variables: map[string]interface{}{
		"new_ref.name":         "v2.0",
		"new_ref.short":        "2.0",
		"new_ref.sha":          "abc123",
		"new_ref.type":         "tag",
		"new_ref.prev_name":    "v1.9",
		"new_repo.owner":       "octo",
		"new_repo.name":        "demo",
		"new_repo.full_name":   "octo/demo",
		"new_commits.count":    3,
		"new_commits.last_sha": "deadbeef",
	}}

	cases := []struct {
		tmpl, want string
	}{
		{"Release {{ref.short}}", "Release 2.0"},
		{"Tag {{ref.name}} (prev {{ref.prev_name}})", "Tag v2.0 (prev v1.9)"},
		{"sha={{ref.sha}} type={{ref.type}}", "sha=abc123 type=tag"},
		{"repo {{repo.full_name}}", "repo octo/demo"},
		{"shipped {{commits.count}}", "shipped 3"},
		{"head {{commits.last_sha}}", "head deadbeef"},
		// Unknown sub-key leaves the placeholder intact (existing contract).
		{"unknown {{ref.bogus}}", "unknown {{ref.bogus}}"},
		// Unknown top-level namespace also leaves the placeholder intact.
		{"unknown {{somethingelse.x}}", "unknown {{somethingelse.x}}"},
	}
	for _, c := range cases {
		t.Run(c.tmpl, func(t *testing.T) {
			got := as.substituteVariables(c.tmpl, ctx)
			if got != c.want {
				t.Fatalf("substituteVariables(%q) = %q, want %q", c.tmpl, got, c.want)
			}
		})
	}
}

// TestSubstituteVariables_SCMNamespace_ExistingCasesStillWork is a thin
// guard against accidentally regressing the older namespaces when
// adding ref/repo/commits handling.
func TestSubstituteVariables_SCMNamespace_ExistingCasesStillWork(t *testing.T) {
	as := &ActionService{}
	ctx := &models.ExecutionContext{Variables: map[string]interface{}{
		"new_status_id": 42,
		"old_status_id": 17,
		"workspace_id":  9,
	}}
	cases := map[string]string{
		"to {{item.status_id}}":       "to 42",
		"from {{old.status_id}}":      "from 17",
		"ws {{trigger.workspace_id}}": "ws 9",
	}
	for tmpl, want := range cases {
		t.Run(tmpl, func(t *testing.T) {
			got := as.substituteVariables(tmpl, ctx)
			if got != want {
				t.Fatalf("substituteVariables(%q) = %q, want %q", tmpl, got, want)
			}
		})
	}
}
