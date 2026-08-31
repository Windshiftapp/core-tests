//go:build test

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"windshift/internal/auth"
	"windshift/internal/middleware"
	"windshift/internal/testutils"
)

// portalVisibilitySubmitFixture holds restricted and open request types.
type portalVisibilitySubmitFixture struct {
	db               *testutils.TestDB
	handler          *PortalHandler
	allowedUserID    int
	disallowedUserID int
	restrictedTypeID int
	openTypeID       int
}

func newPortalVisibilitySubmitFixture(t *testing.T) *portalVisibilitySubmitFixture {
	t.Helper()
	db := testutils.CreateTestDB(t, true)
	if !testutils.IsPostgres() {
		t.Cleanup(func() { _ = db.Close() })
	}

	var workspaceID, itemTypeID, allowedUserID, disallowedUserID, allowedGroupID, disallowedGroupID, channelID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Portal visibility submit', 'PVISUB') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO item_types (name) VALUES ('Portal visibility submit request') RETURNING id`).Scan(&itemTypeID); err != nil {
		t.Fatalf("insert item type: %v", err)
	}
	for _, u := range []struct {
		email    string
		username string
		dst      *int
	}{
		{"allowed@example.test", "portal-allowed", &allowedUserID},
		{"disallowed@example.test", "portal-disallowed", &disallowedUserID},
	} {
		if err := db.QueryRow(`
			INSERT INTO users (email, username, first_name, last_name, is_active)
			VALUES (?, ?, 'Portal', 'Submitter', true) RETURNING id
		`, u.email, u.username).Scan(u.dst); err != nil {
			t.Fatalf("insert user %s: %v", u.email, err)
		}
	}
	for _, g := range []struct {
		name string
		dst  *int
	}{
		{"Allowed group", &allowedGroupID},
		{"Other group", &disallowedGroupID},
	} {
		if err := db.QueryRow(`INSERT INTO groups (name, is_active) VALUES (?, true) RETURNING id`, g.name).Scan(g.dst); err != nil {
			t.Fatalf("insert group %s: %v", g.name, err)
		}
	}
	for groupID, userID := range map[int]int{allowedGroupID: allowedUserID, disallowedGroupID: disallowedUserID} {
		if _, err := db.ExecWrite(`INSERT INTO group_members (group_id, user_id, added_by) VALUES (?, ?, ?)`, groupID, userID, allowedUserID); err != nil {
			t.Fatalf("insert group member: %v", err)
		}
	}
	config := fmt.Sprintf(`{"portal_slug":"visibility-submit","portal_workspace_ids":[%d]}`, workspaceID)
	if err := db.QueryRow(`
		INSERT INTO channels (name, type, direction, status, config, public_slug)
		VALUES ('Visibility submit portal', 'portal', 'inbound', 'enabled', ?, 'visibility-submit') RETURNING id
	`, config).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}

	insertRequestType := func(name string, visibility any) int {
		t.Helper()
		var id int
		if err := db.QueryRow(`
			INSERT INTO request_types (channel_id, name, item_type_id, workspace_id, is_active, visibility_group_ids)
			VALUES (?, ?, ?, ?, ?, ?) RETURNING id
		`, channelID, name, itemTypeID, workspaceID, true, visibility).Scan(&id); err != nil {
			t.Fatalf("insert request type %s: %v", name, err)
		}
		return id
	}
	restrictedID := insertRequestType("Restricted", fmt.Sprintf(`[%d]`, allowedGroupID))
	openID := insertRequestType("Open", nil)
	// A default title field keeps items.title on the form, so submissions
	// reach item creation instead of the hidden-title fallback.
	for _, requestTypeID := range []int{restrictedID, openID} {
		if _, err := db.ExecWrite(`
			INSERT INTO request_type_fields (request_type_id, field_identifier, field_type, is_required, display_order)
			VALUES (?, 'title', 'default', true, 0)
		`, requestTypeID); err != nil {
			t.Fatalf("insert title field for request type %d: %v", requestTypeID, err)
		}
	}

	return &portalVisibilitySubmitFixture{
		db: db, handler: NewPortalHandler(db, nil, nil, nil, ""),
		allowedUserID: allowedUserID, disallowedUserID: disallowedUserID,
		restrictedTypeID: restrictedID, openTypeID: openID,
	}
}

// submit posts a request type ID as an internal user.
func (f *portalVisibilitySubmitFixture) submit(t *testing.T, userID, requestTypeID int) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"request_type_id": requestTypeID,
		"title":           "Hidden type probe",
		"description":     "attempted submission",
		"custom_fields":   map[string]any{},
	})
	if err != nil {
		t.Fatalf("marshal submission: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/portal/visibility-submit/submit", bytes.NewReader(body))
	req.SetPathValue("slug", "visibility-submit")
	session := &auth.Session{UserID: userID}
	req = req.WithContext(context.WithValue(req.Context(), middleware.ContextKeySession, session))
	rec := httptest.NewRecorder()
	f.handler.SubmitToPortal(rec, req)
	return rec
}

// itemCount counts persisted items for a creator and request type.
func (f *portalVisibilitySubmitFixture) itemCount(t *testing.T, creatorID, requestTypeID int) int {
	t.Helper()
	var n int
	if err := f.db.QueryRow(`SELECT COUNT(*) FROM items WHERE creator_id = ? AND request_type_id = ?`, creatorID, requestTypeID).Scan(&n); err != nil {
		t.Fatalf("count items: %v", err)
	}
	return n
}

func TestPortalVisibilitySubmitRejectsHiddenRequestType(t *testing.T) {
	fixture := newPortalVisibilitySubmitFixture(t)
	rec := fixture.submit(t, fixture.disallowedUserID, fixture.restrictedTypeID)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	var denial struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &denial); err != nil {
		t.Fatalf("decode denial: %v; body=%s", err, rec.Body.String())
	}
	if denial.Error != "Request type not found" {
		t.Fatalf("error = %q, want %q", denial.Error, "Request type not found")
	}
	if got := fixture.itemCount(t, fixture.disallowedUserID, fixture.restrictedTypeID); got != 0 {
		t.Fatalf("items persisted by disallowed user for hidden request type = %d, want 0", got)
	}
}

func TestPortalVisibilitySubmitAllowsRestrictedRequestTypeForAllowedGroup(t *testing.T) {
	fixture := newPortalVisibilitySubmitFixture(t)
	rec := fixture.submit(t, fixture.allowedUserID, fixture.restrictedTypeID)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if got := fixture.itemCount(t, fixture.allowedUserID, fixture.restrictedTypeID); got != 1 {
		t.Fatalf("items persisted by allowed user = %d, want 1", got)
	}
}

func TestPortalVisibilitySubmitAllowsOpenRequestType(t *testing.T) {
	fixture := newPortalVisibilitySubmitFixture(t)
	rec := fixture.submit(t, fixture.disallowedUserID, fixture.openTypeID)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	if got := fixture.itemCount(t, fixture.disallowedUserID, fixture.openTypeID); got != 1 {
		t.Fatalf("items persisted for open request type = %d, want 1", got)
	}
}
