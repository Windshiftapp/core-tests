//go:build test

package handlers

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"windshift/internal/blobstore"
	"windshift/internal/models"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

// onePixelPNG is a valid 1x1 PNG. DetectContentType reports image/png and the
// image-only upload path accepts it.
var onePixelPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a,
	0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
	0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x0a, 0x49, 0x44, 0x41,
	0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00,
	0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae,
	0x42, 0x60, 0x82,
}

func customerAvatarUploadRequest(t *testing.T, entityID string, user *models.User) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("entity_id", entityID); err != nil {
		t.Fatalf("write entity_id: %v", err)
	}
	if err := writer.WriteField("category", "customer_avatar"); err != nil {
		t.Fatalf("write category: %v", err)
	}
	part, err := writer.CreateFormFile("file", "avatar.png")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(onePixelPNG); err != nil {
		t.Fatalf("write png: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/attachments/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return testutils.WithAuthContext(req, user)
}

// TestAttachmentUpload_CustomerAvatar pins the create/edit upload contract for
// organisation avatars. The organisation does not exist yet while creating it,
// so a staged upload with entity_id 0 must be allowed for a caller holding
// customers.manage; a real entity_id must still name an existing organisation.
func TestAttachmentUpload_CustomerAvatar(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	db := tdb.GetDatabase()

	managerID := testutils.InsertID(t, db, `INSERT INTO users (email, username, first_name, last_name) VALUES ('avatar-manager@example.test', 'avatar-manager', 'Avatar', 'Manager')`)
	if _, err := db.ExecWrite(`INSERT INTO user_global_permissions (user_id, permission_id) SELECT ?, id FROM permissions WHERE permission_key = 'customers.manage'`, managerID); err != nil {
		t.Fatalf("grant customers.manage: %v", err)
	}
	manager := &models.User{ID: managerID, Username: "avatar-manager", IsActive: true}

	otherID := testutils.InsertID(t, db, `INSERT INTO users (email, username, first_name, last_name) VALUES ('avatar-other@example.test', 'avatar-other', 'Avatar', 'Other')`)
	other := &models.User{ID: otherID, Username: "avatar-other", IsActive: true}

	customerID := testutils.InsertID(t, db, `INSERT INTO customer_organisations (name) VALUES ('Acme')`)

	permService, err := services.NewPermissionService(db, services.PermissionCacheConfig{
		TTL: time.Minute, MaxCacheSize: 8, WarmupOnStartup: false, PreWarmActive: false, BatchSize: 10,
	})
	if err != nil {
		t.Fatalf("NewPermissionService: %v", err)
	}
	t.Cleanup(func() { _ = permService.Close() })

	handler := NewAttachmentHandler(db, blobstore.NewLocal(t.TempDir()), permService)

	upload := func(entityID string, user *models.User) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		handler.Upload(rec, customerAvatarUploadRequest(t, entityID, user))
		return rec
	}

	t.Run("staged upload before the organisation exists succeeds", func(t *testing.T) {
		rec := upload("0", manager)
		if rec.Code != http.StatusOK {
			t.Fatalf("want 200, got %d (body=%s)", rec.Code, rec.Body.String())
		}
		var resp struct {
			Success    bool   `json:"success"`
			AvatarURL  string `json:"avatar_url"`
			Attachment int64  `json:"attachment_id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v", err)
		}
		if !resp.Success || resp.AvatarURL == "" {
			t.Fatalf("unexpected response: %s", rec.Body.String())
		}

		var entityType string
		var itemID sql.NullInt64
		if err := db.QueryRow(`SELECT entity_type, item_id FROM attachments WHERE id = ?`, resp.Attachment).Scan(&entityType, &itemID); err != nil {
			t.Fatalf("load attachment: %v", err)
		}
		if entityType != "customer_avatar" {
			t.Fatalf("entity_type = %q, want customer_avatar", entityType)
		}
		if itemID.Valid {
			t.Fatalf("item_id = %d, want NULL", itemID.Int64)
		}
	})

	t.Run("upload for an existing organisation succeeds", func(t *testing.T) {
		rec := upload(strconv.Itoa(customerID), manager)
		if rec.Code != http.StatusOK {
			t.Fatalf("want 200, got %d (body=%s)", rec.Code, rec.Body.String())
		}
	})

	t.Run("unknown organisation is rejected", func(t *testing.T) {
		rec := upload("999999", manager)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("want 404, got %d (body=%s)", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "customer not found") {
			t.Fatalf("body = %s, want customer not found", rec.Body.String())
		}
	})

	t.Run("missing customers.manage is denied", func(t *testing.T) {
		rec := upload("0", other)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("want 404, got %d (body=%s)", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "customer not found") {
			t.Fatalf("body = %s, want customer not found", rec.Body.String())
		}
	})
}
