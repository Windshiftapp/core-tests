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

// TestOutput_LinkRendersWorkspaceKeyNumber is the regression for WI-1930:
// item endpoints must render as workspace keys (WI-1928), never as the global
// DB id, which the API also returns as source_id/target_id.
func TestOutput_LinkRendersWorkspaceKeyNumber(t *testing.T) {
	sourceNumber, targetNumber := 1927, 1928

	t.Run("list table", func(t *testing.T) {
		resp := &LinkListResponse{Outgoing: []ItemLink{{
			ID: 525, LinkTypeName: "Relates To", LinkTypeForwardLabel: "relates to",
			SourceType: "item", SourceID: 3000, SourceWorkspaceKey: "WI", SourceItemNumber: &sourceNumber,
			TargetType: "item", TargetID: 3001, TargetWorkspaceKey: "WI", TargetItemNumber: &targetNumber,
			TargetTitle: "Migrate homepage and dashboard data from v1 to v2 API",
		}}}
		out := captureStdout(t, func() { (&Output{format: "table"}).Print(resp) })
		if !strings.Contains(out, "WI-1928") {
			t.Errorf("expected workspace key WI-1928 in table output:\n%s", out)
		}
		if strings.Contains(out, "WI-3001") {
			t.Errorf("table output leaked global DB id as a key:\n%s", out)
		}
	})

	t.Run("list csv", func(t *testing.T) {
		resp := &LinkListResponse{Incoming: []ItemLink{{
			ID: 524, LinkTypeName: "Relates To", LinkTypeReverseLabel: "relates to",
			SourceType: "item", SourceID: 3002, SourceWorkspaceKey: "WI", SourceItemNumber: ptrInt(1929),
			TargetType: "item", TargetID: 3000, TargetWorkspaceKey: "WI", TargetItemNumber: &sourceNumber,
			SourceTitle: "Rethink notification delivery for agent identities",
		}}}
		out := captureStdout(t, func() { (&Output{format: "csv"}).Print(resp) })
		if !strings.Contains(out, "WI-1929") || strings.Contains(out, "WI-3002") {
			t.Errorf("csv output did not render incoming source as WI-1929:\n%s", out)
		}
	})

	t.Run("detail table", func(t *testing.T) {
		link := &ItemLink{
			ID: 525, LinkTypeName: "Relates To", LinkTypeForwardLabel: "relates to",
			SourceType: "item", SourceID: 3000, SourceWorkspaceKey: "WI", SourceItemNumber: &sourceNumber,
			TargetType: "item", TargetID: 3001, TargetWorkspaceKey: "WI", TargetItemNumber: &targetNumber,
		}
		out := captureStdout(t, func() { (&Output{format: "table"}).Print(link) })
		if !strings.Contains(out, "WI-1927") || !strings.Contains(out, "WI-1928") {
			t.Errorf("detail table missing workspace keys:\n%s", out)
		}
		if strings.Contains(out, "WI-3000") || strings.Contains(out, "WI-3001") {
			t.Errorf("detail table leaked global DB ids as keys:\n%s", out)
		}
	})

	t.Run("missing item number falls back to opaque id", func(t *testing.T) {
		link := &ItemLink{
			ID: 1, LinkTypeName: "Relates To", LinkTypeForwardLabel: "relates to",
			SourceType: "item", SourceID: 3000, SourceWorkspaceKey: "WI",
			TargetType: "item", TargetID: 3001, TargetWorkspaceKey: "WI",
		}
		out := captureStdout(t, func() { (&Output{format: "table"}).Print(link) })
		if strings.Contains(out, "WI-3001") {
			t.Errorf("missing item number must not fall back to a workspace key:\n%s", out)
		}
		if !strings.Contains(out, "item:3001") {
			t.Errorf("expected opaque item:3001 fallback:\n%s", out)
		}
	})
}

func ptrInt(v int) *int { return &v }
