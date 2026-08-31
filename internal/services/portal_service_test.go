package services

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"windshift/internal/database"
)

// Regression test for docs/bughunt-2026-05-19-pass-2.md F6.
//
// GetCustomFieldsForChannel must not return custom-field definitions whose
// only references live on request types or asset reports that the caller
// can't see via portal visibility rules. Otherwise an authenticated portal
// user can enumerate the names, descriptions, and option vocabularies of
// internal/hidden fields by hitting `/portal/{slug}/custom-fields`.

func newPortalServiceTestDB(t *testing.T) database.Database {
	t.Helper()
	dsn := fmt.Sprintf("file:portal-svc-%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := database.NewSQLiteDB(dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("init schema: %v", err)
	}
	return db
}

func seedCustomFieldDef(t *testing.T, db database.Database, name string) int {
	t.Helper()
	res, err := db.Exec(`
		INSERT INTO custom_field_definitions (name, field_type, required, options, display_order, system_default, applies_to_portal_customers, applies_to_customer_organisations)
		VALUES (?, 'text', false, '[]', 0, false, false, false)
	`, name)
	if err != nil {
		t.Fatalf("seed cfd %s: %v", name, err)
	}
	id, _ := res.LastInsertId()
	return int(id)
}

func seedChannel(t *testing.T, db database.Database) (int, int) {
	t.Helper()
	workspaceResult, err := db.Exec(`INSERT INTO workspaces (name, key) VALUES ('Portal Service', 'PSVC')`)
	if err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	workspaceID64, _ := workspaceResult.LastInsertId()
	workspaceID := int(workspaceID64)

	var itemTypeID int
	if err := db.QueryRow(`SELECT id FROM item_types LIMIT 1`).Scan(&itemTypeID); err != nil {
		t.Fatalf("no item_type: %v", err)
	}
	configResult, err := db.Exec(`INSERT INTO configuration_sets (name) VALUES ('Portal Service Config')`)
	if err != nil {
		t.Fatalf("seed configuration set: %v", err)
	}
	configID64, _ := configResult.LastInsertId()
	screenResult, err := db.Exec(`INSERT INTO screens (name) VALUES ('Portal Service Create')`)
	if err != nil {
		t.Fatalf("seed create screen: %v", err)
	}
	screenID64, _ := screenResult.LastInsertId()
	if _, err := db.Exec(`INSERT INTO workspace_configuration_sets (workspace_id, configuration_set_id) VALUES (?, ?)`, workspaceID, configID64); err != nil {
		t.Fatalf("bind workspace configuration: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO configuration_set_item_types (configuration_set_id, item_type_id, create_screen_id)
		VALUES (?, ?, ?)
	`, configID64, itemTypeID, screenID64); err != nil {
		t.Fatalf("bind create screen: %v", err)
	}

	res, err := db.Exec(`
		INSERT INTO channels (name, type, direction, status, config)
		VALUES ('TestPortal', 'portal', 'inbound', 'enabled', ?)
	`, fmt.Sprintf(`{"portal_workspace_ids":[%d]}`, workspaceID))
	if err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	id, _ := res.LastInsertId()
	return int(id), workspaceID
}

func seedRequestTypeForPortal(t *testing.T, db database.Database, channelID, workspaceID int, visibilityGroupIDs, visibilityOrgIDs []int) int {
	t.Helper()
	var itemTypeID int
	if err := db.QueryRow(`SELECT id FROM item_types LIMIT 1`).Scan(&itemTypeID); err != nil {
		t.Fatalf("no item_type: %v", err)
	}
	groups, _ := json.Marshal(visibilityGroupIDs)
	orgs, _ := json.Marshal(visibilityOrgIDs)
	res, err := db.Exec(`
		INSERT INTO request_types (channel_id, name, item_type_id, workspace_id, config, is_active, visibility_group_ids, visibility_org_ids)
		VALUES (?, ?, ?, ?, '{}', true, ?, ?)
	`, channelID, fmt.Sprintf("RT-%d-%d", channelID, len(visibilityGroupIDs)+len(visibilityOrgIDs)), itemTypeID, workspaceID, string(groups), string(orgs))
	if err != nil {
		t.Fatalf("seed request_type: %v", err)
	}
	id, _ := res.LastInsertId()
	return int(id)
}

func attachCustomFieldToRT(t *testing.T, db database.Database, rtID, cfID int) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO request_type_fields (request_type_id, field_identifier, field_type, is_required, display_order)
		VALUES (?, ?, 'custom', false, 0)
	`, rtID, fmt.Sprintf("%d", cfID)); err != nil {
		t.Fatalf("attach cf %d to rt %d: %v", cfID, rtID, err)
	}
	if _, err := db.Exec(`
		INSERT INTO screen_fields (screen_id, field_identifier, field_type, display_order)
		SELECT csit.create_screen_id, ?, 'custom', 0
		FROM request_types rt
		JOIN workspace_configuration_sets wcs ON wcs.workspace_id = rt.workspace_id
		JOIN configuration_set_item_types csit
		  ON csit.configuration_set_id = wcs.configuration_set_id
		 AND csit.item_type_id = rt.item_type_id
		WHERE rt.id = ?
	`, fmt.Sprintf("%d", cfID), rtID); err != nil {
		t.Fatalf("bind cf %d to create screen: %v", cfID, err)
	}
}

// F6 happy path: an unrestricted request type's custom field is visible to
// everyone, but a request type restricted to group [42] hides its custom
// field from a portal user who is not in group 42.
func TestPortalService_GetCustomFieldsForChannel_FiltersByVisibility(t *testing.T) {
	db := newPortalServiceTestDB(t)
	channelID, workspaceID := seedChannel(t, db)

	publicCFID := seedCustomFieldDef(t, db, "Public Field")
	hiddenCFID := seedCustomFieldDef(t, db, "Internal Only")

	publicRT := seedRequestTypeForPortal(t, db, channelID, workspaceID, nil, nil)
	hiddenRT := seedRequestTypeForPortal(t, db, channelID, workspaceID, []int{42}, nil)
	attachCustomFieldToRT(t, db, publicRT, publicCFID)
	attachCustomFieldToRT(t, db, hiddenRT, hiddenCFID)

	svc := NewPortalService(db)

	// Customer not in group 42, no admin: must see only the public field.
	fields, err := svc.GetCustomFieldsForChannel(context.Background(), channelID, nil, nil, false)
	if err != nil {
		t.Fatalf("GetCustomFieldsForChannel: %v", err)
	}
	if len(fields) != 1 || fields[0].ID != publicCFID {
		t.Fatalf("non-admin user must see only public field; got %+v", fields)
	}

	// Group member: now also sees the restricted field.
	fields, err = svc.GetCustomFieldsForChannel(context.Background(), channelID, []int{42}, nil, false)
	if err != nil {
		t.Fatalf("GetCustomFieldsForChannel (member): %v", err)
	}
	gotIDs := map[int]bool{}
	for _, f := range fields {
		gotIDs[f.ID] = true
	}
	if !gotIDs[publicCFID] || !gotIDs[hiddenCFID] {
		t.Fatalf("group-42 member must see both fields; got ids %v", gotIDs)
	}

	// Admin: sees everything regardless of group membership.
	fields, err = svc.GetCustomFieldsForChannel(context.Background(), channelID, nil, nil, true)
	if err != nil {
		t.Fatalf("GetCustomFieldsForChannel (admin): %v", err)
	}
	gotIDs = map[int]bool{}
	for _, f := range fields {
		gotIDs[f.ID] = true
	}
	if !gotIDs[publicCFID] || !gotIDs[hiddenCFID] {
		t.Fatalf("admin must see both fields; got ids %v", gotIDs)
	}
}

// F6 customer-org variant: a request type restricted to org 7 hides its
// fields from a portal customer whose customer_organisation_id is something
// else, and surfaces them to a member of org 7.
func TestPortalService_GetCustomFieldsForChannel_FiltersByCustomerOrg(t *testing.T) {
	db := newPortalServiceTestDB(t)
	channelID, workspaceID := seedChannel(t, db)

	orgCFID := seedCustomFieldDef(t, db, "Org7 Only")
	orgRT := seedRequestTypeForPortal(t, db, channelID, workspaceID, nil, []int{7})
	attachCustomFieldToRT(t, db, orgRT, orgCFID)

	svc := NewPortalService(db)

	// Different org → hidden.
	otherOrg := 99
	fields, err := svc.GetCustomFieldsForChannel(context.Background(), channelID, nil, &otherOrg, false)
	if err != nil {
		t.Fatalf("GetCustomFieldsForChannel: %v", err)
	}
	if len(fields) != 0 {
		t.Fatalf("customer in org 99 must not see org-7-restricted field; got %+v", fields)
	}

	// Matching org → visible.
	memberOrg := 7
	fields, err = svc.GetCustomFieldsForChannel(context.Background(), channelID, nil, &memberOrg, false)
	if err != nil {
		t.Fatalf("GetCustomFieldsForChannel (org member): %v", err)
	}
	if len(fields) != 1 || fields[0].ID != orgCFID {
		t.Fatalf("customer in org 7 must see org-7 field; got %+v", fields)
	}
}
