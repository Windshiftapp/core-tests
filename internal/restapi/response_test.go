//go:build test

package restapi

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

// TestNormalizeEmptySlice covers the list-encoding contract: a nil top-level
// slice becomes an empty slice so it encodes as [] instead of null, while
// non-slice and populated values are left untouched.
func TestNormalizeEmptySlice(t *testing.T) {
	tests := []struct {
		name    string
		in      any
		wantNil bool
		wantLen int
	}{
		{name: "nil interface", in: nil, wantNil: true},
		{name: "nil slice", in: []int(nil), wantLen: 0},
		{name: "empty slice", in: []int{}, wantLen: 0},
		{name: "populated slice", in: []int{1, 2}, wantLen: 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeEmptySlice(tc.in)
			if tc.wantNil {
				if got != nil {
					t.Fatalf("NormalizeEmptySlice(nil) = %#v, want nil", got)
				}
				return
			}
			slice, ok := got.([]int)
			if !ok {
				t.Fatalf("NormalizeEmptySlice(%#v) = %T, want []int", tc.in, got)
			}
			if slice == nil {
				t.Fatalf("NormalizeEmptySlice(%#v) returned a nil slice", tc.in)
			}
			if len(slice) != tc.wantLen {
				t.Fatalf("len = %d, want %d", len(slice), tc.wantLen)
			}
		})
	}
}

// TestRespondJSONNormalizesNilSlice pins the wire shape for list endpoints.
func TestRespondJSONNormalizesNilSlice(t *testing.T) {
	rec := httptest.NewRecorder()
	RespondJSON(rec, 200, []string(nil))
	if got := rec.Body.String(); got != "[]\n" {
		t.Fatalf("RespondJSON(nil slice) = %q, want []", got)
	}
}

// TestRespondPaginatedNormalizesNilSlice covers the wrapped page envelope, where
// the list lives under "data".
func TestRespondPaginatedNormalizesNilSlice(t *testing.T) {
	rec := httptest.NewRecorder()
	RespondPaginated(rec, []string(nil), PaginationMeta{Page: 1, Limit: 20})

	var body struct {
		Data       []string       `json:"data"`
		Pagination PaginationMeta `json:"pagination"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Data == nil {
		t.Fatalf("paginated nil slice encoded as null, want []")
	}
	if len(body.Data) != 0 {
		t.Fatalf("data length = %d, want 0", len(body.Data))
	}
}
