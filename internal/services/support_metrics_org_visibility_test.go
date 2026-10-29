//go:build test

package services

import (
	"context"
	"fmt"
	"testing"
	"time"

	"windshift/internal/testutils"
)

// TestSupportMetricsOrganisationSegmentHidesRestrictedOrgs pins the org
// whitelist on support-metrics segmentation: a workspace viewer who is not on a
// restricted organisation's whitelist must not see that org's id, name, or
// aggregate statistics, even though its tickets appear in a visible workspace.
func TestSupportMetricsOrganisationSegmentHidesRestrictedOrgs(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()

	config := DefaultPermissionCacheConfig()
	config.WarmupOnStartup = false
	permissions, err := NewPermissionService(db, config)
	if err != nil {
		t.Fatalf("permission service: %v", err)
	}
	t.Cleanup(func() { _ = permissions.Close() })
	application := NewItemApplicationService(db, permissions,
		NewItemCreationService(db, permissions),
		NewItemUpdateApplicationService(db, permissions),
		NewItemDeletionApplicationService(db, permissions),
	)

	insertOrg := func(name string) int {
		t.Helper()
		var id int
		if err := db.QueryRow(`INSERT INTO customer_organisations (name) VALUES (?) RETURNING id`, name).Scan(&id); err != nil {
			t.Fatalf("insert org %s: %v", name, err)
		}
		return id
	}
	openOrg := insertOrg("Open Org")
	restrictedOrg := insertOrg("Restricted Org")

	// Restricting an org means whitelisting at least one member; the caller is
	// not that member.
	otherUser := testutils.InsertID(t, db, `
		INSERT INTO users (email, username, first_name, last_name, is_active)
		VALUES ('org-other@example.test', 'org-other', 'Other', 'User', true)
	`)
	if _, err := db.Exec(`
		INSERT INTO customer_organisation_members (customer_organisation_id, member_type, member_id)
		VALUES (?, 'user', ?)
	`, restrictedOrg, otherUser); err != nil {
		t.Fatalf("restrict org: %v", err)
	}

	insertCustomer := func(email string, orgID int) int {
		t.Helper()
		var id int
		if err := db.QueryRow(`
			INSERT INTO portal_customers (name, email, customer_organisation_id)
			VALUES (?, ?, ?) RETURNING id
		`, email, email, orgID).Scan(&id); err != nil {
			t.Fatalf("insert customer %s: %v", email, err)
		}
		return id
	}
	openCustomer := insertCustomer("open-org@example.test", openOrg)
	restrictedCustomer := insertCustomer("restricted-org@example.test", restrictedOrg)

	now := time.Now().UTC()
	insertTicket := func(number, customerID int) {
		t.Helper()
		if _, err := db.Exec(`
			INSERT INTO items (workspace_id, workspace_item_number, title, description, creator_portal_customer_id, frac_index, created_at, updated_at)
			VALUES (?, ?, 'ticket', '', ?, ?, ?, ?)
		`, data.WorkspaceID, number, customerID, testutils.NextTestFracIndex(), now, now); err != nil {
			t.Fatalf("insert ticket: %v", err)
		}
	}
	insertTicket(1, openCustomer)
	insertTicket(2, restrictedCustomer)

	doc, err := application.AggregateSupportMetrics(context.Background(), SupportMetricsQuery{
		UserID:       data.UserID,
		WorkspaceIDs: []int{data.WorkspaceID},
		From:         now.Add(-24 * time.Hour),
		To:           now.Add(24 * time.Hour),
		Location:     time.UTC,
		Bucket:       SupportBucketDay,
		Segment:      SupportSegmentOrganisation,
	})
	if err != nil {
		t.Fatalf("AggregateSupportMetrics: %v", err)
	}

	labels := make(map[string]string, len(doc.Segments))
	for _, segment := range doc.Segments {
		labels[segment.Key] = segment.Label
	}
	if labels[fmt.Sprintf("%d", openOrg)] != "Open Org" {
		t.Fatalf("segments = %#v, want the open org present", doc.Segments)
	}
	if _, ok := labels[fmt.Sprintf("%d", restrictedOrg)]; ok {
		t.Fatalf("segments = %#v, restricted org disclosed", doc.Segments)
	}
}
