//go:build test

package handlers

import (
	"testing"

	"windshift/internal/models"
)

// TestPatchRequestHasMemberOps verifies every members shape triggers the
// authorization-cache invalidation path, including the path-less value object.
func TestPatchRequestHasMemberOps(t *testing.T) {
	cases := []struct {
		name string
		ops  []models.SCIMPatchOp
		want bool
	}{
		{"path members", []models.SCIMPatchOp{{Op: "add", Path: "members"}}, true},
		{"path filter", []models.SCIMPatchOp{{Op: "remove", Path: `members[value eq "1"]`}}, true},
		{"case folded", []models.SCIMPatchOp{{Op: "add", Path: "Members"}}, true},
		{
			"path-less object",
			[]models.SCIMPatchOp{{Op: "replace", Value: map[string]any{"members": []any{}}}},
			true,
		},
		{"display name only", []models.SCIMPatchOp{{Op: "replace", Path: "displayName", Value: "x"}}, false},
		{"empty", nil, false},
	}
	for _, tc := range cases {
		if got := patchRequestHasMemberOps(tc.ops); got != tc.want {
			t.Fatalf("%s: got %v want %v", tc.name, got, tc.want)
		}
	}
}

// TestGroupMemberIDs covers member parsing and malformed-entry skipping.
func TestGroupMemberIDs(t *testing.T) {
	ids, ok := groupMemberIDs([]any{
		map[string]any{"value": "1"},
		map[string]any{"value": "2", "display": "Two"},
		map[string]any{"value": "not-a-number"},
		map[string]any{"display": "no value"},
		"not-an-object",
	})
	if !ok {
		t.Fatal("array value must parse")
	}
	if len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
		t.Fatalf("ids = %#v want [1 2]", ids)
	}

	if _, ok := groupMemberIDs("not-an-array"); ok {
		t.Fatal("non-array value must report ok=false")
	}
}
