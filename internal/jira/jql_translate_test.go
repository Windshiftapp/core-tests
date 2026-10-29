//go:build test

package jira

import (
	"strings"
	"testing"
)

func TestTranslateJQLNestedBooleanPreservesGrouping(t *testing.T) {
	ql, unsupported := TranslateJQLToWindshiftQL(
		`project = X AND (status = Open OR status = Closed) AND priority = Highest`, 7)
	if len(unsupported) != 0 {
		t.Fatalf("unsupported = %#v, want none", unsupported)
	}
	for _, want := range []string{
		`workspace_id = 7`,
		`status = "Open"`,
		`status = "Closed"`,
		`OR`,
		`priority = "Critical"`,
	} {
		if !strings.Contains(ql, want) {
			t.Fatalf("ql = %q, want to contain %q", ql, want)
		}
	}
}

func TestTranslateJQLTopLevelOrAndNot(t *testing.T) {
	orQL, unsupported := TranslateJQLToWindshiftQL(`status = Open OR status = Done`, 3)
	if len(unsupported) != 0 {
		t.Fatalf("OR unsupported = %#v", unsupported)
	}
	if !strings.Contains(orQL, `status = "Open" OR status = "Done"`) {
		t.Fatalf("OR ql = %q", orQL)
	}

	notQL, unsupported := TranslateJQLToWindshiftQL(`NOT status = Done`, 3)
	if len(unsupported) != 0 {
		t.Fatalf("NOT unsupported = %#v", unsupported)
	}
	if !strings.Contains(notQL, `NOT (status = "Done")`) {
		t.Fatalf("NOT ql = %q", notQL)
	}
}

func TestTranslateJQLUnsupportedNestedClauseRejectsWholeExpression(t *testing.T) {
	ql, unsupported := TranslateJQLToWindshiftQL(`status = Open AND (labels = a AND sprint = 5)`, 3)
	if len(unsupported) == 0 {
		t.Fatal("expected an unsupported clause")
	}
	// A partial translation would widen the selector, so only the workspace
	// guard may remain.
	if strings.Contains(ql, `status = "Open"`) {
		t.Fatalf("ql = %q, must not partially translate an unsupported expression", ql)
	}
	if !strings.Contains(ql, `workspace_id = 3`) {
		t.Fatalf("ql = %q, want the workspace guard", ql)
	}
}

func TestTranslateJQLRequestTypeAndOrganization(t *testing.T) {
	ql, unsupported := TranslateJQLToWindshiftQL(`request type = "Bug report" AND organization = Acme`, 9)
	if len(unsupported) != 0 {
		t.Fatalf("unsupported = %#v, want none", unsupported)
	}
	for _, want := range []string{
		`requesttypename = "Bug report"`,
		`customerorganisation = "Acme"`,
	} {
		if !strings.Contains(ql, want) {
			t.Fatalf("ql = %q, want to contain %q", ql, want)
		}
	}
}
