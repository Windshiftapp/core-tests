//go:build test

package services

import (
	"strings"
	"testing"
)

// TestFailedContentOutcomeDetails verifies WI-1564's reporting fix: the pack
// apply stage must surface bundle entities that failed to import instead of
// marking content ok and reporting the pack applied.
func TestFailedContentOutcomeDetails(t *testing.T) {
	outcomes := []WorkspaceBundleImportOutcome{
		{Entity: "page", Ref: "page-1", Name: "Missing parent page", Status: "failed", Detail: "parent ref \"page-9\" not found in bundle"},
		{Entity: "item", Ref: "item-1", Name: "Healthy item", Status: "imported"},
		{Entity: "item", Ref: "item-2", Name: "", Status: "failed", Detail: "item type not available"},
	}

	details := failedContentOutcomeDetails(outcomes)
	if len(details) != 2 {
		t.Fatalf("failed details = %v, want 2 entries", details)
	}
	if !strings.Contains(details[0], "Missing parent page") || !strings.Contains(details[0], "parent ref") {
		t.Fatalf("first detail = %q, want the page name and reason", details[0])
	}
	if !strings.Contains(details[1], "item-2") || !strings.Contains(details[1], "item type not available") {
		t.Fatalf("second detail = %q, want the ref and reason", details[1])
	}

	if got := failedContentOutcomeDetails([]WorkspaceBundleImportOutcome{
		{Entity: "page", Status: "imported"},
	}); len(got) != 0 {
		t.Fatalf("clean outcomes produced failures: %v", got)
	}
}
