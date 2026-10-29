//go:build test

package services

import (
	"context"
	"fmt"
	"testing"

	"windshift/internal/database"
	"windshift/internal/testutils"
)

// TestPortalRequestVisibilityForLinkedIntakes pins the WI-1547/WI-1644 rules at
// the service layer: My Requests returns the portal's own items plus items
// created through enabled intakes linked to the portal, and nothing else —
// disabled intakes, intakes linked to another portal, internal intakes, and
// items outside served workspaces stay hidden.
func TestPortalRequestVisibilityForLinkedIntakes(t *testing.T) {
	tdb := testutils.CreateTestDB(t, false)
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	ctx := context.Background()

	var otherWorkspaceID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Other WS', 'OVIS') RETURNING id`).Scan(&otherWorkspaceID); err != nil {
		t.Fatalf("insert other workspace: %v", err)
	}

	portalChannelID := insertVisibilityPortal(t, db, "Portal",
		`{"portal_workspace_ids":[`+fmt.Sprintf("%d", data.WorkspaceID)+`]}`)
	otherPortalID := insertVisibilityPortal(t, db, "Other portal", "")
	itemTypeID := visibilityDefaultItemTypeID(t, db)
	customerID := insertVisibilityCustomer(t, db, "Vis Customer", "vis@example.com")

	linkedMailbox := insertVisibilityMailbox(t, db, "Linked mailbox")
	linkedIntake := insertVisibilityIntake(t, db, linkedMailbox, data.WorkspaceID, "INBOX", &portalChannelID, "enabled", itemTypeID)
	linkedTicket := insertVisibilityTicket(t, db, linkedMailbox, &linkedIntake, data.WorkspaceID, customerID, 102, "linked intake ticket")

	disabledMailbox := insertVisibilityMailbox(t, db, "Disabled mailbox")
	disabledIntake := insertVisibilityIntake(t, db, disabledMailbox, data.WorkspaceID, "INBOX", &portalChannelID, "disabled", itemTypeID)
	disabledTicket := insertVisibilityTicket(t, db, disabledMailbox, &disabledIntake, data.WorkspaceID, customerID, 103, "disabled-intake ticket")

	otherMailbox := insertVisibilityMailbox(t, db, "Other mailbox")
	otherIntake := insertVisibilityIntake(t, db, otherMailbox, data.WorkspaceID, "INBOX", &otherPortalID, "enabled", itemTypeID)
	unlinkedTicket := insertVisibilityTicket(t, db, otherMailbox, &otherIntake, data.WorkspaceID, customerID, 104, "other-portal intake ticket")

	internalMailbox := insertVisibilityMailbox(t, db, "Internal mailbox")
	internalIntake := insertVisibilityIntake(t, db, internalMailbox, data.WorkspaceID, "INBOX", nil, "enabled", itemTypeID)
	internalTicket := insertVisibilityTicket(t, db, internalMailbox, &internalIntake, data.WorkspaceID, customerID, 105, "internal intake ticket")

	wrongWorkspaceTicket := insertVisibilityTicket(t, db, linkedMailbox, &linkedIntake, otherWorkspaceID, customerID, 106, "wrong-workspace ticket")
	portalTicket := insertVisibilityTicket(t, db, portalChannelID, nil, data.WorkspaceID, customerID, 101, "portal ticket")

	service := NewPortalService(db)
	requests, err := service.GetRequestsByPortalCustomerID(ctx, customerID, portalChannelID)
	if err != nil {
		t.Fatalf("GetRequestsByPortalCustomerID: %v", err)
	}
	got := map[int]bool{}
	for _, r := range requests {
		got[r.ID] = true
	}
	if !got[portalTicket] || !got[linkedTicket] {
		t.Fatalf("own portal ticket and linked-intake ticket must be visible; got %v (want %d and %d)",
			got, portalTicket, linkedTicket)
	}
	for hidden, why := range map[int]string{
		disabledTicket:       "disabled intake",
		unlinkedTicket:       "intake linked to another portal",
		internalTicket:       "internal intake",
		wrongWorkspaceTicket: "item outside served workspaces",
	} {
		if got[hidden] {
			t.Fatalf("ticket %d visible but must be hidden (%s)", hidden, why)
		}
	}

	// Ownership mirrors the listing: the linked-intake ticket is readable and
	// commentable through the portal; the hidden ones are 404-material.
	if ok, err := service.VerifyRequestOwnership(ctx, linkedTicket, portalChannelID, nil, &customerID); err != nil || !ok {
		t.Fatalf("linked-intake ticket ownership = %v, %v; want true, nil", ok, err)
	}
	for hidden, why := range map[int]string{
		disabledTicket:       "disabled intake",
		unlinkedTicket:       "intake linked to another portal",
		internalTicket:       "internal intake",
		wrongWorkspaceTicket: "item outside served workspaces",
	} {
		if ok, err := service.VerifyRequestOwnership(ctx, hidden, portalChannelID, nil, &customerID); err != nil || ok {
			t.Fatalf("hidden ticket %d ownership = %v, %v; want false, nil (%s)", hidden, ok, err, why)
		}
	}

	// Ownership still requires the caller to be the creator: another customer
	// cannot read the linked-intake ticket through this portal.
	otherCustomerID := insertVisibilityCustomer(t, db, "Other", "other-vis@example.com")
	if ok, err := service.VerifyRequestOwnership(ctx, linkedTicket, portalChannelID, nil, &otherCustomerID); err != nil || ok {
		t.Fatalf("other-customer ownership = %v, %v; want false, nil", ok, err)
	}
}

// TestPortalRequestVisibilityIgnoresLegacyMailboxPortalLink is the WI-1713
// regression: a mailbox upgraded from the pre-intake email_connected_portal_id
// config keeps that stale key, but intake provenance must govern visibility.
// The linked intake stays visible; a sibling internal intake on the same
// mailbox must not inherit the legacy mailbox-level portal link.
func TestPortalRequestVisibilityIgnoresLegacyMailboxPortalLink(t *testing.T) {
	tdb := testutils.CreateTestDB(t, false)
	data := tdb.SeedTestData(t)
	db := tdb.GetDatabase()
	ctx := context.Background()

	portalChannelID := insertVisibilityPortal(t, db, "Portal",
		`{"portal_workspace_ids":[`+fmt.Sprintf("%d", data.WorkspaceID)+`]}`)
	itemTypeID := visibilityDefaultItemTypeID(t, db)
	customerID := insertVisibilityCustomer(t, db, "Scoped Customer", "scoped@example.com")

	// The legacy key the intake split leaves behind in the channel config.
	mailboxID := insertVisibilityMailbox(t, db, "Upgraded mailbox",
		`{"email_connected_portal_id":`+fmt.Sprintf("%d", portalChannelID)+`}`)

	portalIntake := insertVisibilityIntake(t, db, mailboxID, data.WorkspaceID, "Support", &portalChannelID, "enabled", itemTypeID)
	internalIntake := insertVisibilityIntake(t, db, mailboxID, data.WorkspaceID, "Alerts", nil, "enabled", itemTypeID)

	portalTicket := insertVisibilityTicket(t, db, mailboxID, &portalIntake, data.WorkspaceID, customerID, 201, "portal-intake ticket")
	internalTicket := insertVisibilityTicket(t, db, mailboxID, &internalIntake, data.WorkspaceID, customerID, 202, "internal-intake ticket")

	service := NewPortalService(db)
	requests, err := service.GetRequestsByPortalCustomerID(ctx, customerID, portalChannelID)
	if err != nil {
		t.Fatalf("GetRequestsByPortalCustomerID: %v", err)
	}
	got := map[int]bool{}
	for _, r := range requests {
		got[r.ID] = true
	}
	if !got[portalTicket] {
		t.Fatalf("portal-intake ticket %d must be visible", portalTicket)
	}
	if got[internalTicket] {
		t.Fatalf("internal-intake ticket %d leaked into the portal via the legacy mailbox link", internalTicket)
	}

	if ok, err := service.VerifyRequestOwnership(ctx, portalTicket, portalChannelID, nil, &customerID); err != nil || !ok {
		t.Fatalf("portal-intake ownership = %v, %v; want true, nil", ok, err)
	}
	if ok, err := service.VerifyRequestOwnership(ctx, internalTicket, portalChannelID, nil, &customerID); err != nil || ok {
		t.Fatalf("internal-intake ownership = %v, %v; want false, nil", ok, err)
	}
}

func visibilityDefaultItemTypeID(t *testing.T, db database.Database) int {
	t.Helper()
	var id int
	if err := db.QueryRow(`SELECT id FROM item_types WHERE is_default = true ORDER BY id LIMIT 1`).Scan(&id); err != nil {
		t.Fatalf("load default item type: %v", err)
	}
	return id
}

func insertVisibilityPortal(t *testing.T, db database.Database, name, configJSON string) int {
	t.Helper()
	var id int
	var err error
	if configJSON == "" {
		err = db.QueryRow(`
			INSERT INTO channels (name, type, direction, status)
			VALUES (?, 'portal', 'inbound', 'enabled') RETURNING id
		`, name).Scan(&id)
	} else {
		err = db.QueryRow(`
			INSERT INTO channels (name, type, direction, status, config)
			VALUES (?, 'portal', 'inbound', 'enabled', ?) RETURNING id
		`, name, configJSON).Scan(&id)
	}
	if err != nil {
		t.Fatalf("insert portal %s: %v", name, err)
	}
	return id
}

func insertVisibilityMailbox(t *testing.T, db database.Database, name string, configJSON ...string) int {
	t.Helper()
	config := "{}"
	if len(configJSON) > 0 {
		config = configJSON[0]
	}
	var id int
	if err := db.QueryRow(`
		INSERT INTO channels (name, type, direction, status, config)
		VALUES (?, 'email', 'inbound', 'enabled', ?) RETURNING id
	`, name, config).Scan(&id); err != nil {
		t.Fatalf("insert mailbox %s: %v", name, err)
	}
	return id
}

func insertVisibilityIntake(t *testing.T, db database.Database, mailboxID, workspaceID int, folder string, portalChannelID *int, status string, itemTypeID int) int {
	t.Helper()
	var portal any
	if portalChannelID != nil {
		portal = *portalChannelID
	}
	var id int
	if err := db.QueryRow(`
		INSERT INTO intakes (mailbox_id, folder, workspace_id, portal_channel_id, item_type_id, status)
		VALUES (?, ?, ?, ?, ?, ?) RETURNING id
	`, mailboxID, folder, workspaceID, portal, itemTypeID, status).Scan(&id); err != nil {
		t.Fatalf("insert intake %s: %v", folder, err)
	}
	return id
}

func insertVisibilityTicket(t *testing.T, db database.Database, channelID int, intakeID *int, workspaceID, customerID, number int, title string) int {
	t.Helper()
	var intake any
	if intakeID != nil {
		intake = *intakeID
	}
	var id int
	if err := db.QueryRow(`
		INSERT INTO items (workspace_id, workspace_item_number, title, description, channel_id, intake_id, creator_portal_customer_id, frac_index)
		VALUES (?, ?, ?, 'd', ?, ?, ?, ?) RETURNING id
	`, workspaceID, number, title, channelID, intake, customerID, testutils.NextTestFracIndex()).Scan(&id); err != nil {
		t.Fatalf("insert ticket %q: %v", title, err)
	}
	return id
}

func insertVisibilityCustomer(t *testing.T, db database.Database, name, email string) int {
	t.Helper()
	var id int
	if err := db.QueryRow(`INSERT INTO portal_customers (name, email) VALUES (?, ?) RETURNING id`, name, email).Scan(&id); err != nil {
		t.Fatalf("insert customer %s: %v", name, err)
	}
	return id
}
