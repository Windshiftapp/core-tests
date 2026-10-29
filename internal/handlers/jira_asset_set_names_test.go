package handlers

import (
	"testing"

	"windshift/internal/jira"
)

func TestJiraAssetSetNamesDisambiguatesDuplicateSchemaNames(t *testing.T) {
	t.Parallel()

	schemas := []jira.AssetObjectSchema{
		{ID: "1", Name: "CMDB", ObjectSchemaKey: "CMDB"},
		{ID: "2", Name: "CMDB", ObjectSchemaKey: "ITSM"},
		{ID: "3", Name: "Facilities", ObjectSchemaKey: "FAC"},
	}

	names := jiraAssetSetNames(schemas)

	if got, want := names["1"], "Jira Assets: CMDB (CMDB)"; got != want {
		t.Fatalf("names[1] = %q, want %q", got, want)
	}
	if got, want := names["2"], "Jira Assets: CMDB (ITSM)"; got != want {
		t.Fatalf("names[2] = %q, want %q", got, want)
	}
	if got, want := names["3"], "Jira Assets: Facilities"; got != want {
		t.Fatalf("names[3] = %q, want %q", got, want)
	}
	if names["1"] == names["2"] {
		t.Fatal("duplicate Jira schema names must not reuse one Windshift asset set")
	}
}
