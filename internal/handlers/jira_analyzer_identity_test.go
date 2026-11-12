//go:build test

package handlers

import (
	"testing"

	"windshift/internal/jira"
)

// TestCollectUserSummaryNormalizesDataCenterNameKeyIdentities is the WI-2077
// regression: Data Center users carry name/key rather than accountId, and the
// analysis preview must include them with the same normalized identity the
// execution path uses.
func TestCollectUserSummaryNormalizesDataCenterNameKeyIdentities(t *testing.T) {
	t.Parallel()

	userMap := map[string]JiraUserSummary{}
	collectUserSummary(userMap, &jira.JiraUser{Name: "ada", DisplayName: "Ada Lovelace", EmailAddress: "ada@example.invalid"})
	collectUserSummary(userMap, &jira.JiraUser{Key: "grace-key", DisplayName: "Grace Hopper"})
	// Duplicate sighting of the same name must not add a second entry.
	collectUserSummary(userMap, &jira.JiraUser{Name: "ada", DisplayName: "Ada Lovelace"})
	// Cloud accountId keeps working.
	collectUserSummary(userMap, &jira.JiraUser{AccountID: "acc-cloud", DisplayName: "Cloud User"})
	// Anonymous users are skipped.
	collectUserSummary(userMap, &jira.JiraUser{DisplayName: "No Identifier"})
	collectUserSummary(userMap, nil)

	if len(userMap) != 3 {
		t.Fatalf("user map = %#v, want 3 normalized identities", userMap)
	}
	if userMap["ada"].DisplayName != "Ada Lovelace" || userMap["ada"].Email != "ada@example.invalid" {
		t.Fatalf("name identity = %#v, want Ada with email", userMap["ada"])
	}
	if userMap["grace-key"].DisplayName != "Grace Hopper" {
		t.Fatalf("key identity = %#v, want Grace", userMap["grace-key"])
	}
	if _, ok := userMap["acc-cloud"]; !ok {
		t.Fatalf("cloud identity missing from %#v", userMap)
	}
}
