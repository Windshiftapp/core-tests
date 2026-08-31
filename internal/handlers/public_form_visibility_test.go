//go:build test

package handlers

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestPublicFormIgnoresRequestTypeGroupVisibility documents the behavior of
// form-channel request types: visibility filtering is a portal-only
// construct, so a request type with visibility_group_ids set is still
// submittable by anyone who knows (or guesses) the id.
func TestPublicFormIgnoresRequestTypeGroupVisibility(t *testing.T) {
	fixture := newPublicFormAttachmentFixture(t, t.TempDir(), false)
	if _, err := fixture.db.ExecWrite(
		`UPDATE request_types SET visibility_group_ids = '[999999]' WHERE id = ?`,
		fixture.requestTypeID,
	); err != nil {
		t.Fatalf("restrict request type to unassigned group: %v", err)
	}

	body := fmt.Sprintf(`{
		"request_type_id": %d,
		"title": "Group-restricted probe",
		"description": "submitted with a restricted request type id",
		"custom_fields": {}
	}`, fixture.requestTypeID)
	request := httptest.NewRequest(http.MethodPost, "/api/forms/attachment-form/submit", bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("slug", "attachment-form")
	recorder := httptest.NewRecorder()
	fixture.handler.SubmitForm(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
	}
	if got := fixture.rowCount(t, "items"); got != 1 {
		t.Fatalf("item count = %d, want 1", got)
	}
}
