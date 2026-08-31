//go:build test

package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"windshift/internal/repository"
	"windshift/internal/testutils"
)

func newRequestTypeValidationFixture(t *testing.T) (*RequestTypeHandler, int, int) {
	t.Helper()

	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()

	var itemTypeID int
	if err := db.QueryRow(`SELECT id FROM item_types LIMIT 1`).Scan(&itemTypeID); err != nil {
		t.Fatalf("find seeded item type: %v", err)
	}

	var channelID int
	if err := db.QueryRow(`
		INSERT INTO channels (name, type, direction, status)
		VALUES ('Request type validation channel', 'portal', 'inbound', 'enabled')
		RETURNING id
	`).Scan(&channelID); err != nil {
		t.Fatalf("create validation channel: %v", err)
	}

	var requestTypeID int
	if err := db.QueryRow(`
		INSERT INTO request_types (channel_id, name, item_type_id, config)
		VALUES (?, 'Existing request type', ?, '{}')
		RETURNING id
	`, channelID, itemTypeID).Scan(&requestTypeID); err != nil {
		t.Fatalf("create existing request type: %v", err)
	}

	handler := NewRequestTypeHandler(
		repository.NewRequestTypeRepository(db),
		nil,
		nil,
		repository.NewItemTypeRepository(db),
		nil,
		nil,
	)
	return handler, channelID, requestTypeID
}

func TestRequestTypeHandler_CreateAndUpdate_ValidateSharedBasics(t *testing.T) {
	handler, channelID, requestTypeID := newRequestTypeValidationFixture(t)

	tests := []struct {
		name     string
		handle   testutils.TestHandler
		method   string
		path     string
		body     string
		expected string
	}{
		{
			name:     "create requires name",
			handle:   handler.Create,
			method:   http.MethodPost,
			path:     fmt.Sprintf("/api/channels/%d/request-types", channelID),
			body:     `{"item_type_id":1}`,
			expected: "Request type name is required",
		},
		{
			name:     "create requires item type",
			handle:   handler.Create,
			method:   http.MethodPost,
			path:     fmt.Sprintf("/api/channels/%d/request-types", channelID),
			body:     `{"name":"Support"}`,
			expected: "Item type ID is required",
		},
		{
			name:     "create rejects unknown item type",
			handle:   handler.Create,
			method:   http.MethodPost,
			path:     fmt.Sprintf("/api/channels/%d/request-types", channelID),
			body:     `{"name":"Support","item_type_id":999999999}`,
			expected: "Item type not found",
		},
		{
			name:     "update requires name",
			handle:   handler.Update,
			method:   http.MethodPut,
			path:     fmt.Sprintf("/api/channels/%d/request-types/%d", channelID, requestTypeID),
			body:     `{"item_type_id":1}`,
			expected: "Request type name is required",
		},
		{
			name:     "update requires item type",
			handle:   handler.Update,
			method:   http.MethodPut,
			path:     fmt.Sprintf("/api/channels/%d/request-types/%d", channelID, requestTypeID),
			body:     `{"name":"Updated support"}`,
			expected: "Item type ID is required",
		},
		{
			name:     "update rejects unknown item type",
			handle:   handler.Update,
			method:   http.MethodPut,
			path:     fmt.Sprintf("/api/channels/%d/request-types/%d", channelID, requestTypeID),
			body:     `{"name":"Updated support","item_type_id":999999999}`,
			expected: "Item type not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := testutils.CreateJSONRequest(t, tt.method, tt.path, json.RawMessage(tt.body))
			req.SetPathValue("channel_id", testutils.IntToString(channelID))
			req.SetPathValue("id", testutils.IntToString(requestTypeID))

			rr := testutils.ExecuteAuthenticatedRequest(t, tt.handle, req, nil)
			testutils.AssertValidationError(t, rr, tt.expected)
		})
	}
}
