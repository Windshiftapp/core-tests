//go:build test

package handlers

import (
	"strings"
	"testing"
)

// TestParseSCIMFilter_CaseInsensitiveAttribute covers RFC 7643 §2.1: filter
// attribute names are case-insensitive, so an IdP sending "UserName" or
// "Name.GivenName" must resolve to the same column as the canonical spelling.
func TestParseSCIMFilter_CaseInsensitiveAttribute(t *testing.T) {
	cases := []struct {
		filter    string
		wantWhere string
	}{
		{`UserName eq "john"`, "LOWER(username) = LOWER(?)"},
		{`NAME.GIVENNAME eq "John"`, "LOWER(first_name) = LOWER(?)"},
		{`Name.FamilyName eq "Doe"`, "LOWER(last_name) = LOWER(?)"},
		{`DisplayName co "Research"`, "LOWER(first_name || ' ' || last_name) LIKE LOWER(?) ESCAPE '\\'"},
		{`ExternalId eq "abc"`, "LOWER(scim_external_id) = LOWER(?)"},
		{`EMAILS.VALUE eq "a@b.c"`, "LOWER(email) = LOWER(?)"},
	}
	for _, tc := range cases {
		got, err := ParseSCIMFilter(tc.filter, "User")
		if err != nil {
			t.Fatalf("%q: unexpected error: %v", tc.filter, err)
		}
		if got.WhereClause != tc.wantWhere {
			t.Fatalf("%q: WhereClause %q want %q", tc.filter, got.WhereClause, tc.wantWhere)
		}
	}
}

// TestParseSCIMFilter_CaseInsensitiveOperator verifies operators are accepted
// regardless of case.
func TestParseSCIMFilter_CaseInsensitiveOperator(t *testing.T) {
	got, err := ParseSCIMFilter(`userName EQ "john"`, "User")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got.WhereClause, "username") {
		t.Fatalf("WhereClause %q must reference username", got.WhereClause)
	}
	if len(got.Args) != 1 || got.Args[0] != "john" {
		t.Fatalf("Args %#v want [john]", got.Args)
	}
}

// TestParseSCIMFilter_GroupCaseInsensitive covers the group attribute map.
func TestParseSCIMFilter_GroupCaseInsensitive(t *testing.T) {
	got, err := ParseSCIMFilter(`DisplayName eq "Engineering"`, "Group")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.WhereClause != "LOWER(name) = LOWER(?)" {
		t.Fatalf("WhereClause %q", got.WhereClause)
	}
}

// TestExtractResourceTypeFilter_CaseInsensitive verifies meta.resourceType is
// matched regardless of attribute case while preserving the value.
func TestExtractResourceTypeFilter_CaseInsensitive(t *testing.T) {
	rt, remaining := ExtractResourceTypeFilter(`META.RESOURCETYPE eq "Group" and displayName eq "A"`)
	if rt != "Group" {
		t.Fatalf("resourceType = %q want Group", rt)
	}
	if remaining != `displayName eq "A"` {
		t.Fatalf("remaining = %q", remaining)
	}
}
