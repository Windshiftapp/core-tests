//go:build test

package handlers

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"windshift/internal/auth"
	"windshift/internal/middleware"
	"windshift/internal/testutils"
)

func seedPublicRequestVirtualValidation(t *testing.T) (*testutils.TestDB, int, int, int) {
	t.Helper()
	db := testutils.CreateTestDB(t, true)
	if !testutils.IsPostgres() {
		t.Cleanup(func() { _ = db.Close() })
	}

	var workspaceID, itemTypeID, userID int
	if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Public virtual requests', 'PVR') RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if err := db.QueryRow(`INSERT INTO item_types (name) VALUES ('Public virtual request') RETURNING id`).Scan(&itemTypeID); err != nil {
		t.Fatalf("insert item type: %v", err)
	}
	if err := db.QueryRow(`
		INSERT INTO users (email, username, first_name, last_name, is_active)
		VALUES ('virtual@example.test', 'virtual-user', 'Virtual', 'User', true) RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	insertRequestType := func(channelType, slug string) int {
		t.Helper()
		workspaceConfigKey := channelType + "_workspace_ids"
		slugConfigKey := channelType + "_slug"
		config := fmt.Sprintf(`{%q:%q,%q:[%d]}`, slugConfigKey, slug, workspaceConfigKey, workspaceID)
		var channelID, requestTypeID int
		if err := db.QueryRow(`
			INSERT INTO channels (name, type, direction, status, config, public_slug)
			VALUES (?, ?, 'inbound', 'enabled', ?, ?) RETURNING id
		`, channelType+" channel", channelType, config, slug).Scan(&channelID); err != nil {
			t.Fatalf("insert %s channel: %v", channelType, err)
		}
		if err := db.QueryRow(`
			INSERT INTO request_types (channel_id, name, item_type_id, workspace_id, is_active)
			VALUES (?, ?, ?, ?, true) RETURNING id
		`, channelID, channelType+" request", itemTypeID, workspaceID).Scan(&requestTypeID); err != nil {
			t.Fatalf("insert %s request type: %v", channelType, err)
		}
		if _, err := db.ExecWrite(`
			INSERT INTO request_type_fields
				(request_type_id, field_identifier, field_type, is_required, display_order, virtual_field_type, virtual_field_options)
			VALUES
				(?, 'title', 'default', true, 1, NULL, NULL),
				(?, 'urgency', 'virtual', true, 2, 'select', '[{"value":"low","label":"Low"}]')
		`, requestTypeID, requestTypeID); err != nil {
			t.Fatalf("insert %s request fields: %v", channelType, err)
		}
		return requestTypeID
	}

	return db, insertRequestType("form", "virtual-form"), insertRequestType("portal", "virtual-portal"), userID
}

func TestPublicSubmissionSurfacesRejectInvalidVirtualSelect(t *testing.T) {
	db, formRequestTypeID, portalRequestTypeID, userID := seedPublicRequestVirtualValidation(t)

	tests := []struct {
		name          string
		path          string
		requestTypeID int
		invoke        func(*httptest.ResponseRecorder, *http.Request)
	}{
		{
			name:          "public form",
			path:          "/api/forms/virtual-form/submit",
			requestTypeID: formRequestTypeID,
			invoke: func(recorder *httptest.ResponseRecorder, request *http.Request) {
				request.SetPathValue("slug", "virtual-form")
				NewFormHandler(db, nil, nil, nil, nil).SubmitForm(recorder, request)
			},
		},
		{
			name:          "portal",
			path:          "/api/portal/virtual-portal/submit",
			requestTypeID: portalRequestTypeID,
			invoke: func(recorder *httptest.ResponseRecorder, request *http.Request) {
				request.SetPathValue("slug", "virtual-portal")
				session := &auth.Session{UserID: userID}
				request = request.WithContext(context.WithValue(request.Context(), middleware.ContextKeySession, session))
				NewPortalHandler(db, nil, nil, nil, "").SubmitToPortal(recorder, request)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"request_type_id":%d,"title":"Printer","custom_fields":{"urgency":"critical"}}`, test.requestTypeID)
			request := httptest.NewRequest(http.MethodPost, test.path, bytes.NewBufferString(body))
			request.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			test.invoke(recorder, request)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
			}
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM items`).Scan(&count); err != nil {
				t.Fatalf("count items: %v", err)
			}
			if count != 0 {
				t.Fatalf("item count = %d, want 0 after validation rejection", count)
			}
		})
	}
}
