//go:build test

package handlers

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"windshift/internal/models"
)

// TestRespondJSONEncodesNilSliceAsEmptyArray pins the list-response contract:
// a nil Go slice must reach clients as [] rather than null. Repositories build
// results with `var items []T` and return nil when nothing matches; a JSON null
// crashes clients that iterate the response (WI-1601).
func TestRespondJSONEncodesNilSliceAsEmptyArray(t *testing.T) {
	type item struct{ ID int }

	tests := []struct {
		name string
		data any
		want string
	}{
		{name: "nil slice", data: []item(nil), want: "[]\n"},
		{name: "empty slice", data: []item{}, want: "[]\n"},
		{name: "populated slice", data: []item{{ID: 1}}, want: "[{\"ID\":1}]\n"},
		{name: "nil pointer", data: (*item)(nil), want: "null\n"},
		{name: "nil interface", data: nil, want: "null\n"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			respondJSONOK(rec, tc.data)
			if got := rec.Body.String(); got != tc.want {
				t.Fatalf("respondJSONOK(%v) = %q, want %q", tc.data, got, tc.want)
			}
		})
	}
}

// TestRespondJSONKeepsNestedNilSliceAsNull documents that only the top-level
// value is normalized: a nil slice field inside an object keeps its previous
// encoding, so the change stays scoped to list endpoints.
func TestRespondJSONKeepsNestedNilSliceAsNull(t *testing.T) {
	type wrapper struct {
		Items []int `json:"items"`
	}
	rec := httptest.NewRecorder()
	respondJSONOK(rec, wrapper{})
	if got := rec.Body.String(); got != "{\"items\":null}\n" {
		t.Fatalf("nested nil slice = %q, want {\"items\":null}", got)
	}
}

// TestRespondJSONWithWarningsNormalizesWrappedNilSlice covers the warning
// envelope, where the list is nested under "data".
func TestRespondJSONWithWarningsNormalizesWrappedNilSlice(t *testing.T) {
	rec := httptest.NewRecorder()
	respondJSONOKWithWarnings(rec, []int(nil), []models.APIWarning{{Code: "x", Message: "y"}})

	var body struct {
		Data     []int               `json:"data"`
		Warnings []models.APIWarning `json:"warnings"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Data == nil {
		t.Fatalf("wrapped nil slice encoded as null, want []")
	}
	if len(body.Warnings) != 1 {
		t.Fatalf("warnings = %d, want 1", len(body.Warnings))
	}
}
