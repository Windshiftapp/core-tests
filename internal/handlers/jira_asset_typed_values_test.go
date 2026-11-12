//go:build test

package handlers

import (
	"reflect"
	"testing"

	"windshift/internal/jira"
	"windshift/internal/models"
)

// TestJiraAssetAttributeProvenanceValueKeepsCanonicalTimestamp is the WI-2069
// regression: a DateTime stored in a calendar-date field must keep the exact
// source timestamp as provenance, not the importing user's localized rendering.
func TestJiraAssetAttributeProvenanceValueKeepsCanonicalTimestamp(t *testing.T) {
	t.Parallel()

	attr := jira.AssetObjectAttributeValue{ObjectAttributeValues: []jira.AssetAttributeValue{
		{Value: "2023-05-01T12:34:56.789Z", DisplayValue: "May 1, 2023 12:34 PM"},
	}}
	got, ok := jiraAssetAttributeProvenanceValue(attr)
	if !ok {
		t.Fatal("expected a provenance value")
	}
	if got != "2023-05-01T12:34:56.789Z" {
		t.Fatalf("provenance = %#v, want the canonical timestamp", got)
	}
}

// TestJiraAssetAttributeProvenanceValueKeepsMultipleValues proves multiple
// source values stay a list rather than collapsing into ambiguous text.
func TestJiraAssetAttributeProvenanceValueKeepsMultipleValues(t *testing.T) {
	t.Parallel()

	attr := jira.AssetObjectAttributeValue{ObjectAttributeValues: []jira.AssetAttributeValue{
		{Value: "2023-05-01T12:34:56.789Z", DisplayValue: "May 1, 2023 12:34 PM"},
		{Value: "2024-01-02T03:04:05.000Z", DisplayValue: "Jan 2, 2024 3:04 AM"},
	}}
	got, ok := jiraAssetAttributeProvenanceValue(attr)
	if !ok {
		t.Fatal("expected a provenance value")
	}
	want := []string{"2023-05-01T12:34:56.789Z", "2024-01-02T03:04:05.000Z"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("provenance = %#v, want %#v", got, want)
	}
}

// TestJiraAssetAttributeProvenanceValueFallsBackToDisplayText keeps the
// lossless path usable when the source only supplies display text.
func TestJiraAssetAttributeProvenanceValueFallsBackToDisplayText(t *testing.T) {
	t.Parallel()

	attr := jira.AssetObjectAttributeValue{ObjectAttributeValues: []jira.AssetAttributeValue{
		{DisplayValue: "no raw value"},
	}}
	got, ok := jiraAssetAttributeProvenanceValue(attr)
	if !ok || got != "no raw value" {
		t.Fatalf("provenance = %#v (ok=%t), want the display-text fallback", got, ok)
	}
}

// TestJiraAssetAttributeNativeValueUsesCanonicalValues is the WI-2069
// regression: native asset fields must use the canonical raw value instead of
// locale-formatted display text.
func TestJiraAssetAttributeNativeValueUsesCanonicalValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		attr      jira.AssetObjectAttributeValue
		fieldType string
		want      any
		wantOK    bool
	}{
		{
			name: "number prefers canonical value over localized display",
			attr: jira.AssetObjectAttributeValue{ObjectAttributeValues: []jira.AssetAttributeValue{
				{Value: 1234.5, DisplayValue: "1,234.5"},
			}},
			fieldType: "number",
			want:      1234.5,
			wantOK:    true,
		},
		{
			name: "date normalizes the canonical timestamp to a calendar date",
			attr: jira.AssetObjectAttributeValue{ObjectAttributeValues: []jira.AssetAttributeValue{
				{Value: "2023-05-01T00:00:00.000Z", DisplayValue: "May 1, 2023"},
			}},
			fieldType: "date",
			want:      "2023-05-01",
			wantOK:    true,
		},
		{
			name: "multiple scalar values keep canonical text instead of pretending",
			attr: jira.AssetObjectAttributeValue{ObjectAttributeValues: []jira.AssetAttributeValue{
				{Value: "alpha"},
				{Value: "beta"},
			}},
			fieldType: "number",
			want:      "alpha\nbeta",
			wantOK:    true,
		},
		{
			name: "invalid number is not stored as a fake scalar",
			attr: jira.AssetObjectAttributeValue{ObjectAttributeValues: []jira.AssetAttributeValue{
				{DisplayValue: "not a number"},
			}},
			fieldType: "number",
			wantOK:    false,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, ok := jiraAssetAttributeNativeValue(test.attr, test.fieldType)
			if ok != test.wantOK {
				t.Fatalf("ok = %t, want %t (value %#v)", ok, test.wantOK, got)
			}
			if ok && got != test.want {
				t.Fatalf("value = %#v, want %#v", got, test.want)
			}
		})
	}
}

// TestJiraAssetAttributeFieldTypeDistinguishesDateAndTime pins the DefaultType
// dispatch: Date uses the native date field, Time is preserved as text, and
// DateTime uses the native date with timestamp provenance.
func TestJiraAssetAttributeFieldTypeDistinguishesDateAndTime(t *testing.T) {
	t.Parallel()

	tests := []struct {
		defaultTypeID int
		want          string
	}{
		{defaultTypeID: 1, want: "number"},
		{defaultTypeID: 3, want: "number"},
		{defaultTypeID: 2, want: models.CustomFieldTypeBoolean},
		{defaultTypeID: 4, want: "date"},
		{defaultTypeID: 5, want: "textarea"},
		{defaultTypeID: 6, want: "date"},
		{defaultTypeID: 99, want: "textarea"},
	}
	for _, test := range tests {
		got := jiraAssetAttributeFieldType(jira.AssetObjectAttribute{Type: 0, DefaultTypeID: test.defaultTypeID})
		if got != test.want {
			t.Fatalf("DefaultTypeID %d -> %q, want %q", test.defaultTypeID, got, test.want)
		}
	}
}
