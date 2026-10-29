//go:build test

package services

import (
	"context"
	"encoding/json"
	"testing"
)

// TestPortalServesRequestWorkspaceHonoursServedWorkspaces pins the gate the
// portal approver fallback must apply: an item whose workspace was removed from
// a portal's served list is not served, while items in a served workspace (or a
// portal with no workspace restriction) are.
func TestPortalServesRequestWorkspaceHonoursServedWorkspaces(t *testing.T) {
	env := newApprovalTestEnv(t)
	ctx := context.Background()

	var otherWorkspaceID int
	if err := env.db.QueryRow(`INSERT INTO workspaces (name, key, active) VALUES ('Other', 'OTH', true) RETURNING id`).Scan(&otherWorkspaceID); err != nil {
		t.Fatalf("insert other workspace: %v", err)
	}

	insertPortal := func(name string, workspaceIDs []int) int {
		t.Helper()
		config, err := json.Marshal(map[string]any{"portal_workspace_ids": workspaceIDs})
		if err != nil {
			t.Fatalf("marshal portal config: %v", err)
		}
		var id int
		if err := env.db.QueryRow(`
			INSERT INTO channels (name, type, direction, status, config)
			VALUES (?, 'portal', 'inbound', 'enabled', ?) RETURNING id
		`, name, string(config)).Scan(&id); err != nil {
			t.Fatalf("insert portal channel %s: %v", name, err)
		}
		return id
	}

	svc := NewPortalService(env.db)

	excluded := insertPortal("Excluded", []int{otherWorkspaceID})
	served, err := svc.PortalServesRequestWorkspace(ctx, env.itemID, excluded)
	if err != nil {
		t.Fatalf("PortalServesRequestWorkspace: %v", err)
	}
	if served {
		t.Fatal("item in an unserved workspace reported as served")
	}

	included := insertPortal("Included", []int{env.workspaceID})
	served, err = svc.PortalServesRequestWorkspace(ctx, env.itemID, included)
	if err != nil {
		t.Fatalf("PortalServesRequestWorkspace: %v", err)
	}
	if !served {
		t.Fatal("item in a served workspace reported as not served")
	}

	// An empty served list means the portal serves every workspace.
	open := insertPortal("Open", nil)
	served, err = svc.PortalServesRequestWorkspace(ctx, env.itemID, open)
	if err != nil {
		t.Fatalf("PortalServesRequestWorkspace: %v", err)
	}
	if !served {
		t.Fatal("item not served by an unrestricted portal")
	}
}
