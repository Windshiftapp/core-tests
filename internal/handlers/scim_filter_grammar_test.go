//go:build test

package handlers

import (
	"strings"
	"testing"
)

// TestParseSCIMFilter_Or covers the "or" logical operator.
func TestParseSCIMFilter_Or(t *testing.T) {
	got, err := ParseSCIMFilter(`userName eq "a" or userName eq "b"`, "User")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "(LOWER(username) = LOWER(?) OR LOWER(username) = LOWER(?))"
	if got.WhereClause != want {
		t.Fatalf("WhereClause %q want %q", got.WhereClause, want)
	}
	if len(got.Args) != 2 || got.Args[0] != "a" || got.Args[1] != "b" {
		t.Fatalf("Args %#v want [a b]", got.Args)
	}
}

// TestParseSCIMFilter_AndBindsTighterThanOr verifies precedence.
func TestParseSCIMFilter_AndBindsTighterThanOr(t *testing.T) {
	got, err := ParseSCIMFilter(`active eq true or userName eq "x" and externalId eq "y"`, "User")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "(is_active = ? OR (LOWER(username) = LOWER(?) AND LOWER(scim_external_id) = LOWER(?)))"
	if got.WhereClause != want {
		t.Fatalf("WhereClause %q want %q", got.WhereClause, want)
	}
}

// TestParseSCIMFilter_ParenthesizedGrouping covers explicit grouping.
func TestParseSCIMFilter_ParenthesizedGrouping(t *testing.T) {
	got, err := ParseSCIMFilter(`(active eq true or active eq false) and userName eq "x"`, "User")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "((is_active = ? OR is_active = ?) AND LOWER(username) = LOWER(?))"
	if got.WhereClause != want {
		t.Fatalf("WhereClause %q want %q", got.WhereClause, want)
	}
	if len(got.Args) != 3 {
		t.Fatalf("Args %#v want 3 entries", got.Args)
	}
}

// TestParseSCIMFilter_Not covers the "not" operator.
func TestParseSCIMFilter_Not(t *testing.T) {
	got, err := ParseSCIMFilter(`not (active eq true)`, "User")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.WhereClause != "NOT (is_active = ?)" {
		t.Fatalf("WhereClause %q", got.WhereClause)
	}
}

// TestParseSCIMFilter_ComparisonOperators covers gt/ge/lt/le and pr.
func TestParseSCIMFilter_ComparisonOperators(t *testing.T) {
	cases := []struct {
		filter    string
		wantWhere string
	}{
		{`userName gt "m"`, "LOWER(username) > LOWER(?)"},
		{`userName ge "m"`, "LOWER(username) >= LOWER(?)"},
		{`userName lt "m"`, "LOWER(username) < LOWER(?)"},
		{`userName le "m"`, "LOWER(username) <= LOWER(?)"},
		{`externalId pr`, "(scim_external_id IS NOT NULL AND scim_external_id != '')"},
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

// TestParseSCIMFilter_NullComparison covers eq/ne null.
func TestParseSCIMFilter_NullComparison(t *testing.T) {
	eq, err := ParseSCIMFilter(`externalId eq null`, "User")
	if err != nil {
		t.Fatalf("eq null: %v", err)
	}
	if eq.WhereClause != "scim_external_id IS NULL" {
		t.Fatalf("eq null WhereClause %q", eq.WhereClause)
	}
	ne, err := ParseSCIMFilter(`externalId ne null`, "User")
	if err != nil {
		t.Fatalf("ne null: %v", err)
	}
	if ne.WhereClause != "scim_external_id IS NOT NULL" {
		t.Fatalf("ne null WhereClause %q", ne.WhereClause)
	}
}

// TestParseSCIMFilter_ValuePathTrailingComparison covers the Okta-style
// emails[type eq "work"].value eq "x" selector.
func TestParseSCIMFilter_ValuePathTrailingComparison(t *testing.T) {
	got, err := ParseSCIMFilter(`emails[type eq "work"].value eq "user@example.com"`, "User")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.WhereClause != "LOWER(email) = LOWER(?)" {
		t.Fatalf("WhereClause %q", got.WhereClause)
	}
	if len(got.Args) != 1 || got.Args[0] != "user@example.com" {
		t.Fatalf("Args %#v", got.Args)
	}
}

// TestParseSCIMFilter_ValuePathInnerFilter covers the RFC form where the
// predicate lives inside the brackets.
func TestParseSCIMFilter_ValuePathInnerFilter(t *testing.T) {
	got, err := ParseSCIMFilter(`emails[type eq "work" and value co "@example.com"]`, "User")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got.WhereClause, "LOWER(email) LIKE LOWER(?)") {
		t.Fatalf("WhereClause %q must contain the email predicate", got.WhereClause)
	}
	if len(got.Args) != 1 || got.Args[0] != "%@example.com%" {
		t.Fatalf("Args %#v want one entry wrapping @example.com", got.Args)
	}
}

// TestParseSCIMFilter_ValuePathPresent covers a bare selector.
func TestParseSCIMFilter_ValuePathPresent(t *testing.T) {
	got, err := ParseSCIMFilter(`emails[type eq "work"]`, "User")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.WhereClause != "(email IS NOT NULL AND email != '')" {
		t.Fatalf("WhereClause %q", got.WhereClause)
	}
}

// TestParseSCIMFilter_UnsupportedAttribute rejects unknown attributes.
func TestParseSCIMFilter_UnsupportedAttribute(t *testing.T) {
	if _, err := ParseSCIMFilter(`nickName eq "jd"`, "User"); err == nil {
		t.Fatal("unknown attribute must error")
	}
}

// TestParseSCIMFilter_Malformed rejects incomplete expressions.
func TestParseSCIMFilter_Malformed(t *testing.T) {
	for _, filter := range []string{
		`userName eq`,
		`(userName eq "x"`,
		`userName eq "x" and`,
		`not userName eq "x"`,
		`emails[type eq "work"`,
	} {
		if _, err := ParseSCIMFilter(filter, "User"); err == nil {
			t.Fatalf("filter %q must error", filter)
		}
	}
}
