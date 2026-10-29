//go:build test

package services

import (
	"strings"
	"testing"

	"windshift/internal/database"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

type cannedResponseFixture struct {
	db          database.Database
	service     *CannedResponseService
	workspaceID int
	itemID      int
	actor       AuditActor
}

func newCannedResponseFixture(t *testing.T) *cannedResponseFixture {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	var userID int
	if err := db.QueryRow(`INSERT INTO users (email, username, first_name, last_name) VALUES ('agent@test.local', 'agent', 'Ann', 'Agent') RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	var workspaceID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Canned WS', 'CANNED') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	var itemID int
	if err := db.QueryRow(`INSERT INTO items (workspace_id, workspace_item_number, title, frac_index, creator_id) VALUES (?, 3, 'Printer on fire', ?, ?) RETURNING id`,
		workspaceID, testutils.NextTestFracIndex(), userID).Scan(&itemID); err != nil {
		t.Fatalf("insert item: %v", err)
	}

	service := NewCannedResponseService(
		repository.NewCannedResponseRepository(db),
		repository.NewUserRepository(db),
		repository.NewItemRepository(db),
	)
	return &cannedResponseFixture{
		db: db, service: service,
		workspaceID: workspaceID, itemID: itemID,
		actor: AuditActor{UserID: userID, Username: "agent"},
	}
}

func TestCannedResponseCreateRejectsBlankNameAndBody(t *testing.T) {
	f := newCannedResponseFixture(t)

	t.Run("blank name", func(t *testing.T) {
		if _, err := f.service.Create(f.workspaceID, CannedResponseInput{Name: "   ", Body: "hello"}, f.actor); err != ErrCannedResponseNameRequired {
			t.Fatalf("err = %v, want ErrCannedResponseNameRequired", err)
		}
	})
	t.Run("blank body", func(t *testing.T) {
		if _, err := f.service.Create(f.workspaceID, CannedResponseInput{Name: "greeting"}, f.actor); err != ErrCannedResponseBodyRequired {
			t.Fatalf("err = %v, want ErrCannedResponseBodyRequired", err)
		}
	})
}

func TestCannedResponseCreateDefaultsAndDuplicateNames(t *testing.T) {
	f := newCannedResponseFixture(t)
	created, err := f.service.Create(f.workspaceID, CannedResponseInput{Name: "greeting", Body: "hello"}, f.actor)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !created.IsActive || created.IsPrivate {
		t.Fatalf("defaults wrong: is_active=%v is_private=%v, want true/false", created.IsActive, created.IsPrivate)
	}
	if created.CreatedBy == nil || *created.CreatedBy != f.actor.UserID {
		t.Fatalf("created_by = %v, want %d", created.CreatedBy, f.actor.UserID)
	}
	if _, err := f.service.Create(f.workspaceID, CannedResponseInput{Name: "Greeting", Body: "other"}, f.actor); err != repository.ErrDuplicateEntry {
		t.Fatalf("duplicate err = %v, want ErrDuplicateEntry", err)
	}
}

func TestCannedResponseWorkspaceIsolation(t *testing.T) {
	f := newCannedResponseFixture(t)
	created, err := f.service.Create(f.workspaceID, CannedResponseInput{Name: "greeting", Body: "hello"}, f.actor)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	var otherWorkspaceID int
	if err := f.db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Other WS', 'OTHER') RETURNING id`).Scan(&otherWorkspaceID); err != nil {
		t.Fatalf("insert other workspace: %v", err)
	}
	if _, err := f.service.Get(otherWorkspaceID, created.ID); err != ErrCannedResponseWorkspaceWrong {
		t.Fatalf("cross-workspace get err = %v, want ErrCannedResponseWorkspaceWrong", err)
	}
	if _, err := f.service.Update(otherWorkspaceID, created.ID, CannedResponseUpdate{}, f.actor); err != ErrCannedResponseWorkspaceWrong {
		t.Fatalf("cross-workspace update err = %v, want ErrCannedResponseWorkspaceWrong", err)
	}
	if _, err := f.service.Delete(otherWorkspaceID, created.ID, f.actor); err != ErrCannedResponseWorkspaceWrong {
		t.Fatalf("cross-workspace delete err = %v, want ErrCannedResponseWorkspaceWrong", err)
	}
}

func TestCannedResponseArchiveHidesFromDefaultListButKeepsRow(t *testing.T) {
	f := newCannedResponseFixture(t)
	created, err := f.service.Create(f.workspaceID, CannedResponseInput{Name: "greeting", Body: "hello"}, f.actor)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	archived := false
	updated, err := f.service.Update(f.workspaceID, created.ID, CannedResponseUpdate{IsActive: &archived}, f.actor)
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if updated.IsActive {
		t.Fatal("updated response still active")
	}

	active, err := f.service.List(f.workspaceID, false)
	if err != nil || len(active) != 0 {
		t.Fatalf("active list = %v (err %v), want empty", active, err)
	}
	all, err := f.service.List(f.workspaceID, true)
	if err != nil || len(all) != 1 {
		t.Fatalf("include-archived list = %v (err %v), want 1 row", all, err)
	}
}

func TestCannedResponseRenderForItemGuardsArchivedAndPrivate(t *testing.T) {
	f := newCannedResponseFixture(t)
	public, err := f.service.Create(f.workspaceID, CannedResponseInput{Name: "pub", Body: "Hi {{requester.name}}, re {{ticket.key}}"}, f.actor)
	if err != nil {
		t.Fatalf("create public: %v", err)
	}
	private, err := f.service.Create(f.workspaceID, CannedResponseInput{Name: "priv", Body: "internal note", IsPrivate: true}, f.actor)
	if err != nil {
		t.Fatalf("create private: %v", err)
	}

	rendered, isPrivate, err := f.service.RenderForItem(f.workspaceID, public.ID, f.itemID, false, f.actor)
	if err != nil || isPrivate {
		t.Fatalf("public render err=%v isPrivate=%v, want nil/false", err, isPrivate)
	}
	if !strings.Contains(rendered, "Hi ") {
		t.Fatalf("rendered %q lost body text", rendered)
	}
	if !strings.Contains(rendered, "re ") {
		t.Fatalf("rendered %q did not render ticket.key", rendered)
	}

	// Customer-facing renders must not pull private snippets.
	if _, _, err := f.service.RenderForItem(f.workspaceID, private.ID, f.itemID, false, f.actor); err != ErrCannedResponseArchived {
		t.Fatalf("private render for customer err = %v, want ErrCannedResponseArchived", err)
	}
	// Internal renders may.
	if _, isPrivate, err := f.service.RenderForItem(f.workspaceID, private.ID, f.itemID, true, f.actor); err != nil || !isPrivate {
		t.Fatalf("private render for internal err=%v isPrivate=%v, want nil/true", err, isPrivate)
	}

	// Archived responses are refused for both audiences.
	archived := false
	if _, err := f.service.Update(f.workspaceID, public.ID, CannedResponseUpdate{IsActive: &archived}, f.actor); err != nil {
		t.Fatalf("archive: %v", err)
	}
	if _, _, err := f.service.RenderForItem(f.workspaceID, public.ID, f.itemID, true, f.actor); err != ErrCannedResponseArchived {
		t.Fatalf("archived render err = %v, want ErrCannedResponseArchived", err)
	}
}

func TestCannedResponsePreviewUsesSampleValuesWithoutItem(t *testing.T) {
	f := newCannedResponseFixture(t)
	created, err := f.service.Create(f.workspaceID, CannedResponseInput{Name: "tpl", Body: "{{requester.name}} / {{ticket.key}} / {{agent.name}}"}, f.actor)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rendered, err := f.service.RenderPreview(f.workspaceID, created.ID, 0, f.actor)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if !strings.Contains(rendered, "Sample Requester") || !strings.Contains(rendered, "Support Agent") {
		t.Fatalf("preview = %q, want sample substitution", rendered)
	}
	// Unknown variables stay literal so authors notice typos.
	withUnknown, err := f.service.Create(f.workspaceID, CannedResponseInput{Name: "unk", Body: "{{nope}}"}, f.actor)
	if err != nil {
		t.Fatalf("create unknown: %v", err)
	}
	rendered, err = f.service.RenderPreview(f.workspaceID, withUnknown.ID, 0, f.actor)
	if err != nil || rendered != "{{nope}}" {
		t.Fatalf("unknown variable render = %q (err %v), want literal placeholder", rendered, err)
	}
}

func TestCannedResponseUsageCounter(t *testing.T) {
	f := newCannedResponseFixture(t)
	created, err := f.service.Create(f.workspaceID, CannedResponseInput{Name: "used", Body: "body"}, f.actor)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	f.service.TouchUsage(created.ID)
	f.service.TouchUsage(created.ID)
	got, err := f.service.Get(f.workspaceID, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.UsedCount != 2 || got.LastUsedAt == nil {
		t.Fatalf("used_count=%d last_used_at=%v, want 2 and non-nil", got.UsedCount, got.LastUsedAt)
	}
}
