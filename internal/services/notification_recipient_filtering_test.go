//go:build test

package services

import (
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"windshift/internal/database"
	"windshift/internal/models"
)

// recordingDatabase records the queries issued through it so a test can assert
// the fan-out path does not fall back to per-recipient probes.
type recordingDatabase struct {
	database.Database
	mu      sync.Mutex
	queries []string
}

func (db *recordingDatabase) record(query string) {
	db.mu.Lock()
	db.queries = append(db.queries, query)
	db.mu.Unlock()
}

func (db *recordingDatabase) Query(query string, args ...interface{}) (*sql.Rows, error) {
	db.record(query)
	return db.Database.Query(query, args...)
}

func (db *recordingDatabase) QueryRow(query string, args ...interface{}) *sql.Row {
	db.record(query)
	return db.Database.QueryRow(query, args...)
}

func (db *recordingDatabase) reset() {
	db.mu.Lock()
	db.queries = nil
	db.mu.Unlock()
}

func (db *recordingDatabase) countContaining(substring string) int {
	db.mu.Lock()
	defer db.mu.Unlock()
	count := 0
	for _, query := range db.queries {
		if strings.Contains(query, substring) {
			count++
		}
	}
	return count
}

// Regression: recipient filtering used to resolve a permission snapshot and
// probe users.is_active once per recipient, then resolve the snapshot again for
// the referenced-entity check. A multi-recipient rule with a referenced entity
// must resolve one snapshot per recipient, batch the is_active probe, and still
// return exactly the recipients that may view the referenced item.
func TestDetermineRecipientsResolvesSnapshotOncePerRecipient(t *testing.T) {
	env := newPermTestEnv(t)
	t.Cleanup(func() { _ = env.service.Close() })

	ownerID := env.insertUser("fanout-owner@example.test")
	workspaceID := env.insertWorkspace("FANOUT")
	viewerRoleID := env.roleID("Viewer")

	allowed := []int{
		env.insertUser("fanout-a@example.test"),
		env.insertUser("fanout-b@example.test"),
		env.insertUser("fanout-c@example.test"),
	}
	for _, userID := range allowed {
		env.assignUserWorkspaceRole(userID, workspaceID, viewerRoleID)
	}
	// The explicit Viewer assignments above gate the workspace, so this user
	// has no item.view and must be filtered out.
	deniedID := env.insertUser("fanout-denied@example.test")

	targetID64, err := CreateItem(env.db, ItemCreationParams{WorkspaceID: workspaceID, Title: "Fanout target"})
	if err != nil {
		t.Fatalf("create referenced item: %v", err)
	}
	targetID := int(targetID64)

	recorder := &recordingDatabase{Database: env.db}
	service := NewNotificationService(recorder, nil, env.service, DefaultNotificationServiceConfig())
	t.Cleanup(func() { _ = service.Close() })

	// Warm every candidate's snapshot so the fan-out is a pure cache read.
	for _, userID := range append(slices.Clone(allowed), deniedID) {
		if _, err := env.service.GetUserEffectivePermissions(userID); err != nil {
			t.Fatalf("warm snapshot for user %d: %v", userID, err)
		}
	}

	rule := models.NotificationEventRule{
		ID:               1,
		EventType:        models.EventItemUpdated,
		IsEnabled:        true,
		CustomRecipients: fmt.Sprintf("[%d,%d,%d,%d]", allowed[0], allowed[1], allowed[2], deniedID),
	}
	event := &NotificationEvent{
		EventType:                     models.EventItemUpdated,
		WorkspaceID:                   workspaceID,
		ActorUserID:                   ownerID,
		ItemID:                        targetID,
		ReferencedEntityType:          "item",
		ReferencedEntityID:            targetID,
		ReferencedWorkspaceID:         workspaceID,
		ReferencedWorkspacePermission: models.PermissionItemView,
	}

	before := env.service.GetCacheStats()
	recorder.reset()
	recipients := service.determineRecipients(event, &rule)
	after := env.service.GetCacheStats()

	slices.Sort(recipients)
	if !slices.Equal(recipients, allowed) {
		t.Fatalf("recipients = %v, want %v (denied=%d)", recipients, allowed, deniedID)
	}

	// One snapshot resolution per candidate, reused for the view and
	// referenced-entity checks, with no rebuild.
	if got := after.Misses - before.Misses; got != 0 {
		t.Fatalf("snapshot rebuilds during fan-out = %d, want 0", got)
	}
	if got := after.Hits - before.Hits; got != 4 {
		t.Fatalf("snapshot resolutions during fan-out = %d, want one per candidate (4)", got)
	}

	// users.is_active is probed once, batched, never per recipient.
	if got := recorder.countContaining("is_agent, is_active"); got != 1 {
		t.Fatalf("batched is_agent/is_active probes = %d, want 1", got)
	}
	if got := recorder.countContaining("SELECT is_active FROM users WHERE id = ?"); got != 0 {
		t.Fatalf("per-recipient is_active probes = %d, want 0", got)
	}
}
