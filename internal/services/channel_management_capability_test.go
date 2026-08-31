//go:build test

package services

import (
	"context"
	"testing"

	"windshift/internal/testutils"
)

func TestChannelServiceManagesChannelsAllowsSystemAdminWithoutAssignments(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })

	var userID int
	if err := tdb.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name)
		VALUES ('channel-admin@example.com', 'channel-admin', 'Channel', 'Admin')
		RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := tdb.ExecWrite(`
		INSERT INTO user_global_permissions (user_id, permission_id)
		SELECT ?, id FROM permissions WHERE permission_key = 'system.admin'
	`, userID); err != nil {
		t.Fatalf("grant system administrator: %v", err)
	}

	config := DefaultPermissionCacheConfig()
	config.WarmupOnStartup = false
	permissions, err := NewPermissionService(tdb.GetDatabase(), config)
	if err != nil {
		t.Fatalf("create permission service: %v", err)
	}
	service := NewChannelService(tdb.GetDatabase(), permissions)

	got, err := service.ManagesChannels(context.Background(), userID)
	if err != nil {
		t.Fatalf("ManagesChannels: %v", err)
	}
	if !got {
		t.Fatal("ManagesChannels = false for system administrator, want true")
	}
}
