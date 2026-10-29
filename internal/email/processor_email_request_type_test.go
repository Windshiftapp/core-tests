//go:build test

package email_test

import (
	"context"
	"encoding/json"
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/database"
	"windshift/internal/email"
	"windshift/internal/models"
	"windshift/internal/services"
	"windshift/internal/testutils"
	"windshift/internal/testutils/factory"
)

// portalIntakeEnv is a portal channel plus an inbound email channel linked to
// it, so new mail routes through the portal's system Email request type.
type portalIntakeEnv struct {
	db              database.Database
	processor       *email.Processor
	workspaceID     int
	emailChannelID  int
	portalChannelID int
	config          *models.ChannelConfig
}

func setupPortalIntakeEnv(t *testing.T) *portalIntakeEnv {
	t.Helper()

	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	db := tdb.GetDatabase()

	f := factory.NewTestFactory(db)
	_, workspaceID, err := f.CreateUserAndWorkspace()
	if err != nil {
		t.Fatalf("create user and workspace: %v", err)
	}

	// The test workspace has no configuration set, so every seeded item type is
	// allowed; the intake routes by this explicit workspace and item type.
	var itemTypeID int
	if err := db.QueryRow(`SELECT id FROM item_types WHERE is_default = true ORDER BY id LIMIT 1`).Scan(&itemTypeID); err != nil {
		t.Fatalf("load default item type: %v", err)
	}

	portalCfg, _ := json.Marshal(models.ChannelConfig{
		PortalSlug:         "support",
		PortalWorkspaceIDs: []int{workspaceID},
	})
	var portalChannelID int
	if err := tdb.QueryRow(`
		INSERT INTO channels (name, type, direction, status, config, created_at, updated_at)
		VALUES ('Support Portal', 'portal', 'inbound', 'enabled', ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		RETURNING id
	`, string(portalCfg)).Scan(&portalChannelID); err != nil {
		t.Fatalf("create portal channel: %v", err)
	}

	emailCfg := &models.ChannelConfig{
		EmailConnectedPortalID: &portalChannelID,
		EmailWorkspaceID:       workspaceID,
		EmailItemTypeID:        &itemTypeID,
	}
	emailCfgJSON, _ := json.Marshal(emailCfg)
	var emailChannelID int
	if err := tdb.QueryRow(`
		INSERT INTO channels (name, type, direction, status, config, created_at, updated_at)
		VALUES ('Intake', 'email', 'inbound', 'enabled', ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		RETURNING id
	`, string(emailCfgJSON)).Scan(&emailChannelID); err != nil {
		t.Fatalf("create email channel: %v", err)
	}

	processor := email.NewProcessor(db, blobstore.Disabled())
	processor.SetCommentService(services.NewCommentService(db))

	return &portalIntakeEnv{
		db:              db,
		processor:       processor,
		workspaceID:     workspaceID,
		emailChannelID:  emailChannelID,
		portalChannelID: portalChannelID,
		config:          emailCfg,
	}
}

// Portal intake must create items under the system Email request type so email
// tickets carry a request_type_id and required form fields can never block them.
func TestProcessor_PortalIntake_RoutesThroughEmailRequestType(t *testing.T) {
	env := setupPortalIntakeEnv(t)
	ctx := context.Background()

	msg := newTestEmail("<portal-intake-1@example.com>", "Printer is down", "The office printer stopped working.")
	result, err := env.processor.ProcessEmail(ctx, msg, env.emailChannelID, 0, env.config)
	if err != nil {
		t.Fatalf("ProcessEmail: %v", err)
	}
	if result.Action != email.ActionItemCreated {
		t.Fatalf("action = %s, want %s", result.Action, email.ActionItemCreated)
	}

	var requestTypeID, workspaceID, itemTypeID int
	if err := env.db.QueryRow(
		`SELECT request_type_id, workspace_id, item_type_id FROM items WHERE id = ?`, *result.ItemID,
	).Scan(&requestTypeID, &workspaceID, &itemTypeID); err != nil {
		t.Fatalf("load item: %v", err)
	}
	if workspaceID != env.workspaceID {
		t.Errorf("workspace_id = %d, want %d", workspaceID, env.workspaceID)
	}

	var kind string
	var channelID, rtItemTypeID, fieldCount int
	if err := env.db.QueryRow(`
		SELECT rt.kind, rt.channel_id, rt.item_type_id,
		       (SELECT COUNT(*) FROM request_type_fields f WHERE f.request_type_id = rt.id)
		FROM request_types rt WHERE rt.id = ?
	`, requestTypeID).Scan(&kind, &channelID, &rtItemTypeID, &fieldCount); err != nil {
		t.Fatalf("load request type: %v", err)
	}
	if kind != models.RequestTypeKindEmail {
		t.Errorf("request type kind = %q, want %q", kind, models.RequestTypeKindEmail)
	}
	if channelID != env.portalChannelID {
		t.Errorf("request type channel = %d, want portal %d", channelID, env.portalChannelID)
	}
	if rtItemTypeID != itemTypeID {
		t.Errorf("item item_type_id = %d, want request type item_type_id %d", itemTypeID, rtItemTypeID)
	}
	if fieldCount != 0 {
		t.Errorf("email request type field count = %d, want 0 (required fields must not block email)", fieldCount)
	}
}

// Provisioning is idempotent: a second call returns the same row and never
// creates a duplicate system request type.
func TestEnsureEmailRequestType_Idempotent(t *testing.T) {
	env := setupPortalIntakeEnv(t)

	first, err := services.EnsureEmailRequestType(env.db, env.portalChannelID, 0, nil)
	if err != nil {
		t.Fatalf("first EnsureEmailRequestType: %v", err)
	}
	second, err := services.EnsureEmailRequestType(env.db, env.portalChannelID, 0, nil)
	if err != nil {
		t.Fatalf("second EnsureEmailRequestType: %v", err)
	}
	if first != second {
		t.Errorf("ids differ: first = %d, second = %d", first, second)
	}

	var count int
	if err := env.db.QueryRow(
		`SELECT COUNT(*) FROM request_types WHERE channel_id = ? AND kind = ?`,
		env.portalChannelID, models.RequestTypeKindEmail,
	).Scan(&count); err != nil {
		t.Fatalf("count email request types: %v", err)
	}
	if count != 1 {
		t.Errorf("email request type rows = %d, want 1", count)
	}
}

// A portal that serves no workspace cannot host email intake; provisioning
// fails instead of minting a request type with no routing target.
func TestEnsureEmailRequestType_RequiresServedWorkspace(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })

	cfg, _ := json.Marshal(models.ChannelConfig{PortalSlug: "empty"})
	var portalID int
	if err := tdb.QueryRow(`
		INSERT INTO channels (name, type, direction, status, config, created_at, updated_at)
		VALUES ('Empty Portal', 'portal', 'inbound', 'disabled', ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		RETURNING id
	`, string(cfg)).Scan(&portalID); err != nil {
		t.Fatalf("create portal: %v", err)
	}

	if _, err := services.EnsureEmailRequestType(tdb.GetDatabase(), portalID, 0, nil); err == nil {
		t.Fatal("expected error for portal with no served workspaces, got nil")
	}
}

// A portal that serves several workspaces has no unambiguous email intake
// target. Provisioning must fail rather than pick the first configured
// workspace; an explicit served preference resolves it.
func TestEnsureEmailRequestType_RejectsAmbiguousWorkspace(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	db := tdb.GetDatabase()

	f := factory.NewTestFactory(db)
	userID, wsA, err := f.CreateUserAndWorkspace()
	if err != nil {
		t.Fatalf("create user and workspace: %v", err)
	}
	wsB, err := f.CreateWorkspace(factory.CreateWorkspaceOpts{CreatorID: userID})
	if err != nil {
		t.Fatalf("create second workspace: %v", err)
	}

	cfg, _ := json.Marshal(models.ChannelConfig{PortalSlug: "multi", PortalWorkspaceIDs: []int{wsA, wsB}})
	var portalID int
	if err := tdb.QueryRow(`
		INSERT INTO channels (name, type, direction, status, config, created_at, updated_at)
		VALUES ('Multi Portal', 'portal', 'inbound', 'disabled', ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
		RETURNING id
	`, string(cfg)).Scan(&portalID); err != nil {
		t.Fatalf("create portal: %v", err)
	}

	if _, err := services.EnsureEmailRequestType(db, portalID, 0, nil); err == nil {
		t.Fatal("expected ambiguity error for multi-workspace portal with no preference")
	}
	if _, err := services.EnsureEmailRequestType(db, portalID, wsB, nil); err != nil {
		t.Fatalf("explicit served workspace should resolve intake: %v", err)
	}
}

// The Email request type is an internal routing row, not a customer-selectable
// form: the public request-type validation must reject it.
func TestEnsureEmailRequestType_HiddenFromPublicValidation(t *testing.T) {
	env := setupPortalIntakeEnv(t)
	ctx := context.Background()

	requestTypeID, err := services.EnsureEmailRequestType(env.db, env.portalChannelID, 0, nil)
	if err != nil {
		t.Fatalf("EnsureEmailRequestType: %v", err)
	}

	portalService := services.NewPortalService(env.db)
	ok, err := portalService.ValidateRequestTypeBelongsToChannel(ctx, requestTypeID, env.portalChannelID)
	if err != nil {
		t.Fatalf("ValidateRequestTypeBelongsToChannel: %v", err)
	}
	if ok {
		t.Error("email request type must not validate as a public request type")
	}
}

// A portal-linked item routes by the intake's workspace and item type while the
// system Email request type stays the generic email marker with its own type.
func TestProcessor_PortalIntake_RoutesByIntakeItemType(t *testing.T) {
	env := setupPortalIntakeEnv(t)
	ctx := context.Background()

	first, err := env.processor.ProcessEmail(
		ctx,
		newTestEmail("<override-1@example.com>", "First", "body"),
		env.emailChannelID,
		0,
		env.config,
	)
	if err != nil {
		t.Fatalf("ProcessEmail first: %v", err)
	}
	var requestTypeID, defaultItemTypeID int
	if err := env.db.QueryRow(
		`SELECT request_type_id, item_type_id FROM items WHERE id = ?`, *first.ItemID,
	).Scan(&requestTypeID, &defaultItemTypeID); err != nil {
		t.Fatalf("load first item: %v", err)
	}

	// The test workspace has no configuration set, so every seeded item type is
	// allowed. Pick one that differs from the request type default.
	var overrideItemTypeID int
	if err := env.db.QueryRow(
		`SELECT id FROM item_types WHERE id != ? ORDER BY id LIMIT 1`, defaultItemTypeID,
	).Scan(&overrideItemTypeID); err != nil {
		t.Fatalf("find override item type: %v", err)
	}

	overrideConfig := *env.config
	overrideConfig.EmailItemTypeID = &overrideItemTypeID
	second, err := env.processor.ProcessEmail(
		ctx,
		newTestEmail("<override-2@example.com>", "Second", "body"),
		env.emailChannelID,
		0,
		&overrideConfig,
	)
	if err != nil {
		t.Fatalf("ProcessEmail second: %v", err)
	}

	var secondItemTypeID int
	if err := env.db.QueryRow(`SELECT item_type_id FROM items WHERE id = ?`, *second.ItemID).Scan(&secondItemTypeID); err != nil {
		t.Fatalf("load second item: %v", err)
	}
	if secondItemTypeID != overrideItemTypeID {
		t.Errorf("second item type = %d, want intake override %d", secondItemTypeID, overrideItemTypeID)
	}

	var requestTypeItemTypeID int
	if err := env.db.QueryRow(`SELECT item_type_id FROM request_types WHERE id = ?`, requestTypeID).Scan(&requestTypeItemTypeID); err != nil {
		t.Fatalf("load request type: %v", err)
	}
	if requestTypeItemTypeID != defaultItemTypeID {
		t.Errorf("request type item type = %d, want unchanged %d", requestTypeItemTypeID, defaultItemTypeID)
	}
}
