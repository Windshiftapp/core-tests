package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"windshift/internal/database"
)

func TestPublicFormBootstrapEmbedsSoleFormDetail(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "public-form-bootstrap.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	insertID := func(label, query string, args ...interface{}) int {
		t.Helper()
		var id int
		if err := db.QueryRow(query, args...).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", label, err)
		}
		return id
	}
	workspaceID := insertID("workspace", `INSERT INTO workspaces (name, key) VALUES ('Public forms', 'PF') RETURNING id`)
	itemTypeID := insertID("item type", `INSERT INTO item_types (name) VALUES ('Public form request') RETURNING id`)
	channelConfig := fmt.Sprintf(`{"form_slug":"support","form_theme":"light","form_workspace_ids":[%d]}`, workspaceID)
	channelID := insertID("channel", `
		INSERT INTO channels (name, type, direction, status, config, public_slug)
		VALUES ('Support forms', 'form', 'inbound', 'enabled', ?, 'support') RETURNING id
	`, channelConfig)
	formID := insertID("request type", `
		INSERT INTO request_types (channel_id, name, item_type_id, workspace_id, is_active, config)
		VALUES (?, 'Ask support', ?, ?, true, '{"submit_button_text":"Send"}') RETURNING id
	`, channelID, itemTypeID, workspaceID)
	if _, err := db.ExecWrite(`
		INSERT INTO request_type_fields
			(request_type_id, field_identifier, field_type, is_required, display_order)
		VALUES (?, 'title', 'default', true, 1)
	`, formID); err != nil {
		t.Fatalf("insert form field: %v", err)
	}

	handler := NewFormHandler(db, nil, nil, nil, nil)
	request := httptest.NewRequest(http.MethodGet, "/api/forms/support/bootstrap", nil)
	request.SetPathValue("slug", "support")
	recorder := httptest.NewRecorder()
	handler.GetBootstrap(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var response PublicFormBootstrapResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Channel.ChannelID != channelID || response.Channel.Slug != "support" {
		t.Fatalf("channel = %+v, want channel %d/support", response.Channel, channelID)
	}
	if len(response.Forms) != 1 || response.Forms[0].ID != formID {
		t.Fatalf("forms = %+v, want sole form %d", response.Forms, formID)
	}
	if response.FormDetail == nil || response.FormDetail.FormID != formID {
		t.Fatalf("form detail = %+v, want embedded form %d", response.FormDetail, formID)
	}
	if len(response.FormDetail.Fields) != 1 || response.FormDetail.Fields[0].FieldIdentifier != "title" {
		t.Fatalf("fields = %+v, want title field", response.FormDetail.Fields)
	}
	if response.FormDetail.CustomFieldDefinitions == nil {
		t.Fatal("custom_field_definitions must encode as an empty array, not null")
	}
}

func TestPublicFormDetailRejectsFormFromAnotherChannel(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "public-form-detail-scope.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	var workspaceID, itemTypeID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Scoped forms', 'SF') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO item_types (name) VALUES ('Scoped form request') RETURNING id`).Scan(&itemTypeID); err != nil {
		t.Fatalf("insert item type: %v", err)
	}
	insertChannel := func(name, slug string) int {
		t.Helper()
		config := fmt.Sprintf(`{"form_slug":%q,"form_workspace_ids":[%d]}`, slug, workspaceID)
		var id int
		if err := db.QueryRow(`
			INSERT INTO channels (name, type, direction, status, config, public_slug)
			VALUES (?, 'form', 'inbound', 'enabled', ?, ?) RETURNING id
		`, name, config, slug).Scan(&id); err != nil {
			t.Fatalf("insert channel %s: %v", slug, err)
		}
		return id
	}
	allowedChannelID := insertChannel("Allowed", "allowed")
	otherChannelID := insertChannel("Other", "other")
	var otherFormID int
	if err := db.QueryRow(`
		INSERT INTO request_types (channel_id, name, item_type_id, workspace_id, is_active)
		VALUES (?, 'Other form', ?, ?, true) RETURNING id
	`, otherChannelID, itemTypeID, workspaceID).Scan(&otherFormID); err != nil {
		t.Fatalf("insert other form: %v", err)
	}
	if allowedChannelID == otherChannelID {
		t.Fatal("test channels unexpectedly share an ID")
	}

	handler := NewFormHandler(db, nil, nil, nil, nil)
	request := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/forms/allowed/forms/%d/detail", otherFormID), nil)
	request.SetPathValue("slug", "allowed")
	request.SetPathValue("id", fmt.Sprintf("%d", otherFormID))
	recorder := httptest.NewRecorder()
	handler.GetFormDetail(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", recorder.Code, recorder.Body.String())
	}
}
