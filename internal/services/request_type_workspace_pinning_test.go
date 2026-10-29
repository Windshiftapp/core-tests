//go:build test

package services

import (
	"fmt"
	"strings"
	"testing"

	"windshift/internal/models"
	"windshift/internal/testutils"
)

// WI-1695: a request type pins its workspace at creation, so a channel config
// update that drops that workspace strands the route. The update is rejected
// with guidance instead of silently retargeting or leaving a dead route.
func TestValidateRequestTypeRoutesRejectsRemovingPinnedWorkspace(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	db := tdb.GetDatabase()

	var wsA, wsB int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Route A', 'ROUTEA') RETURNING id`).Scan(&wsA); err != nil {
		t.Fatalf("insert workspace A: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Route B', 'ROUTEB') RETURNING id`).Scan(&wsB); err != nil {
		t.Fatalf("insert workspace B: %v", err)
	}

	var itemTypeID int
	if err := db.QueryRow(`SELECT id FROM item_types ORDER BY id LIMIT 1`).Scan(&itemTypeID); err != nil {
		t.Fatalf("select item type: %v", err)
	}

	config := fmt.Sprintf(`{"portal_slug":"route-portal","portal_workspace_ids":[%d,%d]}`, wsA, wsB)
	var channelID int
	if err := db.QueryRow(`
		INSERT INTO channels (name, type, direction, status, config)
		VALUES ('Route Portal', 'portal', 'inbound', 'enabled', ?) RETURNING id
	`, config).Scan(&channelID); err != nil {
		t.Fatalf("insert portal channel: %v", err)
	}

	if _, err := db.Exec(`
		INSERT INTO request_types (channel_id, name, item_type_id, workspace_id)
		VALUES (?, 'Routed request', ?, ?)
	`, channelID, itemTypeID, wsA); err != nil {
		t.Fatalf("insert request type: %v", err)
	}

	svc := NewChannelConfigUpdateService(NewChannelService(db, nil), nil)

	err := svc.validateRequestTypeRoutes(channelID, "portal", &models.ChannelConfig{PortalWorkspaceIDs: []int{wsB}})
	if err == nil {
		t.Fatal("removing the workspace a request type is pinned to was accepted")
	}
	if !strings.Contains(err.Error(), "Delete them before removing the workspace") {
		t.Fatalf("error = %v, want delete-first guidance", err)
	}

	// Reordering the same workspaces keeps the pinned route served and must not
	// be rejected.
	if err := svc.validateRequestTypeRoutes(channelID, "portal", &models.ChannelConfig{PortalWorkspaceIDs: []int{wsB, wsA}}); err != nil {
		t.Fatalf("reorder of served workspaces unexpectedly rejected: %v", err)
	}
}
