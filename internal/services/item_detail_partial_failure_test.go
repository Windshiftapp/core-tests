//go:build test

package services

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestItemDetailSummaryDistinguishesFailedSections(t *testing.T) {
	summary := ItemDetailSummary{
		SectionErrors: []ItemDetailSectionError{{Section: "links", Code: "unavailable"}},
	}
	encoded, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	text := string(encoded)
	if !strings.Contains(text, `"section_errors":[{"section":"links","code":"unavailable"}]`) {
		t.Fatalf("summary = %s, want stable section failure", text)
	}
}
