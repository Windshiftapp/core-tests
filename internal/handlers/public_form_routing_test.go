//go:build test

package handlers

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func submitPublicFormForRouting(t *testing.T, fixture *publicFormAttachmentFixture) *httptest.ResponseRecorder {
	t.Helper()
	body := fmt.Sprintf(`{
		"request_type_id": %d,
		"title": "Workspace-routed request",
		"description": "Verify the selected workspace",
		"custom_fields": {}
	}`, fixture.requestTypeID)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/forms/attachment-form/submit",
		bytes.NewBufferString(body),
	)
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("slug", "attachment-form")
	recorder := httptest.NewRecorder()
	fixture.handler.SubmitForm(recorder, request)
	return recorder
}

// WI-1695: the implicit first-served-workspace fallback is gone. An unpinned
// request type (legacy data, or a route whose channel serves no workspace) is
// rejected instead of routing to the first workspace.
func TestPublicFormUnpinnedRequestTypeWorkspaceIsRejected(t *testing.T) {
	fixture := newPublicFormAttachmentFixture(t, t.TempDir(), false)

	if _, err := fixture.db.ExecWrite(
		`UPDATE request_types SET workspace_id = NULL WHERE id = ?`,
		fixture.requestTypeID,
	); err != nil {
		t.Fatalf("clear request type workspace: %v", err)
	}

	recorder := submitPublicFormForRouting(t, fixture)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "no target workspace") {
		t.Fatalf("body = %s, want missing-workspace error", recorder.Body.String())
	}
	if got := fixture.rowCount(t, "items"); got != 0 {
		t.Fatalf("item count = %d, want 0 after unpinned routing denial", got)
	}
}

func TestPublicFormUsesPinnedWorkspaceInMultiWorkspaceChannel(t *testing.T) {
	fixture := newPublicFormAttachmentFixture(t, t.TempDir(), false)

	var originalWorkspaceID int
	if err := fixture.db.QueryRow(
		`SELECT workspace_id FROM request_types WHERE id = ?`,
		fixture.requestTypeID,
	).Scan(&originalWorkspaceID); err != nil {
		t.Fatalf("read original workspace: %v", err)
	}
	var pinnedWorkspaceID int
	if err := fixture.db.QueryRow(`
		INSERT INTO workspaces (name, key)
		VALUES ('Pinned public form target', 'PFP') RETURNING id
	`).Scan(&pinnedWorkspaceID); err != nil {
		t.Fatalf("insert pinned workspace: %v", err)
	}
	config := fmt.Sprintf(
		`{"form_slug":"attachment-form","form_workspace_ids":[%d,%d]}`,
		originalWorkspaceID,
		pinnedWorkspaceID,
	)
	if _, err := fixture.db.ExecWrite(
		`UPDATE channels SET config = ? WHERE id = ?`,
		config,
		fixture.channelID,
	); err != nil {
		t.Fatalf("add pinned served workspace: %v", err)
	}
	if _, err := fixture.db.ExecWrite(
		`UPDATE request_types SET workspace_id = ? WHERE id = ?`,
		pinnedWorkspaceID,
		fixture.requestTypeID,
	); err != nil {
		t.Fatalf("pin request type workspace: %v", err)
	}

	recorder := submitPublicFormForRouting(t, fixture)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
	}
	var createdWorkspaceID int
	if err := fixture.db.QueryRow(`SELECT workspace_id FROM items`).Scan(&createdWorkspaceID); err != nil {
		t.Fatalf("read created item workspace: %v", err)
	}
	if createdWorkspaceID != pinnedWorkspaceID {
		t.Fatalf("created workspace = %d, want pinned workspace %d", createdWorkspaceID, pinnedWorkspaceID)
	}
}
