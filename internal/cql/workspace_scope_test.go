//go:build test

package cql

import (
	"errors"
	"testing"
)

func TestExtractWorkspaceScope(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  []WorkspaceScopeReference
	}{
		{
			name:  "generic workspace scope accepts a name or key",
			query: `workspace = "Alpha" AND labels = "public"`,
			want:  []WorkspaceScopeReference{{Field: WorkspaceScopeNameOrKey, Value: "Alpha"}},
		},
		{
			name:  "each OR branch carries a scope",
			query: `(workspaceKey = "ALPHA" AND status = "Open") OR (workspace_id = 2 AND status = "Done")`,
			want: []WorkspaceScopeReference{
				{Field: WorkspaceScopeKey, Value: "ALPHA"},
				{Field: WorkspaceScopeID, Value: "2"},
			},
		},
		{
			name:  "workspace list",
			query: `workspace IN ("Alpha", "Beta")`,
			want: []WorkspaceScopeReference{
				{Field: WorkspaceScopeNameOrKey, Value: "Alpha"},
				{Field: WorkspaceScopeNameOrKey, Value: "Beta"},
			},
		},
		{
			name:  "bare generic workspace scope",
			query: `workspace = Alpha AND labels = "public"`,
			want:  []WorkspaceScopeReference{{Field: WorkspaceScopeNameOrKey, Value: "Alpha"}},
		},
		{
			name:  "outer AND scope bounds an unscoped nested OR",
			query: `workspace_id = 7 AND (labels = "public" OR priority = "high")`,
			want:  []WorkspaceScopeReference{{Field: WorkspaceScopeID, Value: "7"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ExtractWorkspaceScope(tt.query)
			if err != nil {
				t.Fatalf("ExtractWorkspaceScope() error = %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("references = %#v, want %#v", got, tt.want)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("reference %d = %#v, want %#v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestExtractWorkspaceScopeRejectsUnboundedQueries(t *testing.T) {
	queries := []string{
		`labels = "public"`,
		`workspace != "Alpha"`,
		`workspace = "Alpha" OR labels = "public"`,
		`NOT workspace = "Alpha"`,
		`workspace_id NOT IN (1, 2)`,
	}

	for _, query := range queries {
		t.Run(query, func(t *testing.T) {
			_, err := ExtractWorkspaceScope(query)
			if !errors.Is(err, ErrWorkspaceScopeRequired) {
				t.Fatalf("ExtractWorkspaceScope() error = %v, want ErrWorkspaceScopeRequired", err)
			}
		})
	}
}
