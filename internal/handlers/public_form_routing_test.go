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

func TestPublicFormRejectsAmbiguousLegacyWorkspaceRouting(t *testing.T) {
	fixture := newPublicFormAttachmentFixture(t, t.TempDir(), false)

	var originalWorkspaceID int
	if err := fixture.db.QueryRow(
		`SELECT workspace_id FROM request_types WHERE id = ?`,
		fixture.requestTypeID,
	).Scan(&originalWorkspaceID); err != nil {
		t.Fatalf("read original workspace: %v", err)
	}
	var secondWorkspaceID int
	if err := fixture.db.QueryRow(`
		INSERT INTO workspaces (name, key)
		VALUES ('Second public form target', 'PFS') RETURNING id
	`).Scan(&secondWorkspaceID); err != nil {
		t.Fatalf("insert second workspace: %v", err)
	}
	config := fmt.Sprintf(
		`{"form_slug":"attachment-form","form_workspace_ids":[%d,%d]}`,
		originalWorkspaceID,
		secondWorkspaceID,
	)
	if _, err := fixture.db.ExecWrite(
		`UPDATE channels SET config = ? WHERE id = ?`,
		config,
		fixture.channelID,
	); err != nil {
		t.Fatalf("add second served workspace: %v", err)
	}
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
	if !strings.Contains(recorder.Body.String(), "select a target workspace") {
		t.Fatalf("body = %s, want explicit target-workspace error", recorder.Body.String())
	}
	if got := fixture.rowCount(t, "items"); got != 0 {
		t.Fatalf("item count = %d, want 0 after ambiguous routing denial", got)
	}
}

func TestPublicFormPreservesUnambiguousLegacyWorkspaceRouting(t *testing.T) {
	fixture := newPublicFormAttachmentFixture(t, t.TempDir(), false)

	var workspaceID int
	if err := fixture.db.QueryRow(
		`SELECT workspace_id FROM request_types WHERE id = ?`,
		fixture.requestTypeID,
	).Scan(&workspaceID); err != nil {
		t.Fatalf("read original workspace: %v", err)
	}
	if _, err := fixture.db.ExecWrite(
		`UPDATE request_types SET workspace_id = NULL WHERE id = ?`,
		fixture.requestTypeID,
	); err != nil {
		t.Fatalf("clear request type workspace: %v", err)
	}

	recorder := submitPublicFormForRouting(t, fixture)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
	}
	var createdWorkspaceID int
	if err := fixture.db.QueryRow(`SELECT workspace_id FROM items`).Scan(&createdWorkspaceID); err != nil {
		t.Fatalf("read created item workspace: %v", err)
	}
	if createdWorkspaceID != workspaceID {
		t.Fatalf("created workspace = %d, want sole served workspace %d", createdWorkspaceID, workspaceID)
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
