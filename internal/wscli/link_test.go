package wscli

import (
	"strings"
	"testing"
)

// TestParseEntityRefPrefixed covers the prefix-syntax branches that don't
// reach the network. The bare-WI-1 fallback path is integration-tested
// against a live server.
func TestParseEntityRefPrefixed(t *testing.T) {
	cases := []struct {
		in       string
		wantType string
		wantID   int
		wantErr  string
	}{
		{in: "item:42", wantType: "item", wantID: 42},
		{in: "page:7", wantType: "page", wantID: 7},
		{in: "test:5", wantType: "test_case", wantID: 5},
		{in: "test_case:5", wantType: "test_case", wantID: 5},
		{in: "tc:5", wantType: "test_case", wantID: 5},
		{in: "ITEM:9", wantType: "item", wantID: 9}, // case-insensitive prefix
		{in: "", wantErr: "empty"},
		{in: "page:notanint", wantErr: "invalid numeric"},
		{in: "asset:1", wantErr: "unknown entity-type prefix"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			gotType, gotID, err := parseEntityRef(nil, tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want error containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotType != tc.wantType || gotID != tc.wantID {
				t.Fatalf("got (%q,%d) want (%q,%d)", gotType, gotID, tc.wantType, tc.wantID)
			}
		})
	}
}

func TestLinkTypeAllows(t *testing.T) {
	page := &LinkType{Name: "Page", AllowedEntityTypes: []string{"item", "page"}}
	tests := &LinkType{Name: "Tests", AllowedEntityTypes: []string{"item", "test_case"}}
	relates := &LinkType{Name: "Relates To"} // nil ⇒ any

	cases := []struct {
		lt          *LinkType
		src, tgt    string
		wantAllowed bool
	}{
		{page, "item", "page", true},
		{page, "page", "item", true},
		{page, "item", "test_case", false}, // user's "do not mix" constraint
		{page, "page", "page", false},      // CLI mirrors the server's budget check: AllowedEntityTypes=[item,page] only fits one of each
		{tests, "item", "test_case", true},
		{tests, "item", "page", false},
		{relates, "item", "item", true},
		{relates, "item", "page", true}, // nil ⇒ any; server still rejects but that's its job
	}
	for _, tc := range cases {
		got := linkTypeAllows(tc.lt, tc.src, tc.tgt)
		if got != tc.wantAllowed {
			t.Errorf("%s(%s↔%s) = %v, want %v", tc.lt.Name, tc.src, tc.tgt, got, tc.wantAllowed)
		}
	}
}

func TestAutoLinkTypeName(t *testing.T) {
	cases := []struct {
		src, tgt, want string
	}{
		{"item", "page", "Page"},
		{"page", "item", "Page"},
		{"item", "test_case", "Tests"},
		{"test_case", "item", "Tests"},
		{"item", "item", "Relates To"},
		{"page", "test_case", ""}, // no default — CLI must require --type (and none exists anyway)
		{"page", "page", ""},
	}
	for _, tc := range cases {
		if got := autoLinkTypeName(tc.src, tc.tgt); got != tc.want {
			t.Errorf("autoLinkTypeName(%q,%q) = %q, want %q", tc.src, tc.tgt, got, tc.want)
		}
	}
}
