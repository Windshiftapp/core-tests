package handlers

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"windshift/internal/contextkeys"
	"windshift/internal/database"
	"windshift/internal/models"
	"windshift/internal/services"
)

// newAttachmentDeleteTestDB stands up just the tables the Delete handler
// touches when exercising the branding-refusal, NULL-item_id, and
// avatar-ownership branches. We avoid db.Initialize() so this test stays
// focused on the authz contract regardless of upstream schema churn.
func newAttachmentDeleteTestDB(t *testing.T) database.Database {
	t.Helper()
	db, err := database.NewSQLiteDB("file::memory:?cache=shared&mode=memory")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	for _, stmt := range []string{
		`CREATE TABLE attachments (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			item_id INTEGER,
			entity_type TEXT DEFAULT 'item',
			filename TEXT NOT NULL,
			original_filename TEXT NOT NULL,
			file_path TEXT NOT NULL,
			mime_type TEXT NOT NULL,
			file_size INTEGER NOT NULL,
			uploaded_by INTEGER,
			has_thumbnail BOOLEAN DEFAULT FALSE,
			thumbnail_path TEXT,
			category TEXT DEFAULT '',
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE items (id INTEGER PRIMARY KEY, workspace_id INTEGER NOT NULL, creator_portal_customer_id INTEGER)`,
		`CREATE TABLE item_history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			item_id INTEGER NOT NULL,
			user_id INTEGER,
			field_name TEXT,
			old_value TEXT,
			new_value TEXT,
			changed_at DATETIME
		)`,
		`CREATE TABLE audit_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			user_id INTEGER,
			username TEXT NOT NULL,
			ip_address TEXT,
			user_agent TEXT,
			action_type TEXT NOT NULL,
			resource_type TEXT NOT NULL,
			resource_id INTEGER,
			resource_name TEXT,
			details TEXT,
			success BOOLEAN NOT NULL DEFAULT TRUE,
			error_message TEXT
		)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}
	return db
}

// newAttachmentHandler builds a handler whose AttachmentService is wired to
// the test DB but without a permission service (the branches we test never
// reach into permissions).
func newAttachmentHandler(db database.Database) *AttachmentHandler {
	return &AttachmentHandler{
		db:                db,
		attachmentPath:    "/tmp/windshift-test-attachments",
		permissionService: nil,
		attachmentService: services.NewAttachmentServiceWithPermissions(db, nil),
	}
}

// withUser returns a request whose context carries the given user so
// utils.GetCurrentUser / RequireAuth resolve them.
func withUser(r *http.Request, u *models.User) *http.Request {
	ctx := context.WithValue(r.Context(), contextkeys.User, u)
	return r.WithContext(ctx)
}

func newMultipartFieldsRequest(t *testing.T, method, target string, fields map[string]string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("write multipart field: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(method, target, &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

// The resolveStoredAttachmentPath tests moved with the implementation: path
// confinement now lives in internal/fileserve (OpenUnderRoot/RemoveUnderRoot),
// whose tests cover root-prefixed legacy rows, relative rows, traversal, and
// symlink escapes — see internal/fileserve/fileserve_test.go.

func insertAttachment(t *testing.T, db database.Database, entityType string, itemID *int, uploadedBy *int) int {
	t.Helper()
	var row struct{ id int }
	// thumbnail_path defaults to '' (not NULL) because the Thumbnail handler
	// scans it into a plain string; the production schema follows the same
	// convention via the upload path always providing a value.
	err := db.QueryRow(`
		INSERT INTO attachments (item_id, entity_type, filename, original_filename, file_path, mime_type, file_size, uploaded_by, thumbnail_path)
		VALUES (?, ?, 'f.bin', 'f.bin', '/tmp/windshift-test-attachments/f.bin', 'application/octet-stream', 0, ?, '')
		RETURNING id
	`, itemID, entityType, uploadedBy).Scan(&row.id)
	if err != nil {
		t.Fatalf("insert attachment: %v", err)
	}
	return row.id
}

// TestAttachmentDelete_BrandingTypesRefused covers the critical bug: a NULL
// item_id no longer bypasses authorization. Every branding entity_type is
// refused via this endpoint regardless of who calls.
func TestAttachmentUpload_CategoryEntityTypeMismatchRejected(t *testing.T) {
	db := newAttachmentDeleteTestDB(t)
	h := newAttachmentHandler(db)
	user := &models.User{ID: 42, Username: "tester"}

	rec := httptest.NewRecorder()
	req := newMultipartFieldsRequest(t, http.MethodPost, "/attachments/upload", map[string]string{
		"entity_type": "item",
		"category":    "portal_logo",
	})
	req = withUser(req, user)

	h.Upload(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("category/entity_type mismatch: want 400, got %d (body=%s)", rec.Code, rec.Body.String())
	}
}

func TestAttachmentDelete_BrandingTypesRefused(t *testing.T) {
	db := newAttachmentDeleteTestDB(t)
	h := newAttachmentHandler(db)
	user := &models.User{ID: 42, Username: "tester"}

	brandingTypes := []string{
		"workspace_avatar",
		"workspace_background",
		"team_avatar",
		"customer_avatar",
		"portal_background",
		"portal_logo",
		"hub_logo",
	}

	for _, et := range brandingTypes {
		t.Run(et, func(t *testing.T) {
			id := insertAttachment(t, db, et, nil, &user.ID)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodDelete, "/attachments/"+strconvI(id), nil)
			req.SetPathValue("id", strconvI(id))
			req = withUser(req, user)

			h.Delete(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("entity_type=%s: want 404, got %d (body=%s)", et, rec.Code, rec.Body.String())
			}

			// Row must still exist — the refusal must not delete.
			var remaining int
			if err := db.QueryRow(`SELECT COUNT(*) FROM attachments WHERE id = ?`, id).Scan(&remaining); err != nil {
				t.Fatalf("count rows: %v", err)
			}
			if remaining != 1 {
				t.Fatalf("entity_type=%s: row was deleted despite 404", et)
			}
		})
	}
}

// TestAttachmentDelete_AvatarUploaderOnly: the uploader of a user-avatar
// attachment can delete it; another authenticated user cannot.
func TestAttachmentDelete_AvatarUploaderOnly(t *testing.T) {
	db := newAttachmentDeleteTestDB(t)
	h := newAttachmentHandler(db)
	uploader := &models.User{ID: 1, Username: "uploader"}
	other := &models.User{ID: 2, Username: "other"}

	t.Run("non-uploader refused", func(t *testing.T) {
		id := insertAttachment(t, db, "avatar", nil, &uploader.ID)

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodDelete, "/attachments/"+strconvI(id), nil)
		req.SetPathValue("id", strconvI(id))
		req = withUser(req, other)

		h.Delete(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("non-uploader DELETE: want 404, got %d (body=%s)", rec.Code, rec.Body.String())
		}
		var n int
		_ = db.QueryRow(`SELECT COUNT(*) FROM attachments WHERE id = ?`, id).Scan(&n)
		if n != 1 {
			t.Fatalf("avatar was deleted by non-uploader")
		}
	})

	t.Run("uploader allowed", func(t *testing.T) {
		id := insertAttachment(t, db, "avatar", nil, &uploader.ID)

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodDelete, "/attachments/"+strconvI(id), nil)
		req.SetPathValue("id", strconvI(id))
		req = withUser(req, uploader)

		h.Delete(rec, req)

		if rec.Code != http.StatusNoContent {
			t.Fatalf("uploader DELETE: want 204, got %d (body=%s)", rec.Code, rec.Body.String())
		}
		var n int
		_ = db.QueryRow(`SELECT COUNT(*) FROM attachments WHERE id = ?`, id).Scan(&n)
		if n != 0 {
			t.Fatalf("avatar was not deleted by uploader")
		}
	})

	t.Run("avatar with nil uploaded_by refused", func(t *testing.T) {
		// Defensive: an avatar row with no uploader (orphaned) must not be
		// claimable by any caller.
		id := insertAttachment(t, db, "avatar", nil, nil)

		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodDelete, "/attachments/"+strconvI(id), nil)
		req.SetPathValue("id", strconvI(id))
		req = withUser(req, uploader)

		h.Delete(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("nil-uploader avatar DELETE: want 404, got %d (body=%s)", rec.Code, rec.Body.String())
		}
	})
}

// TestAttachmentDelete_ItemTypeNullItemID_Refused: a synthetic item-typed
// row with NULL item_id (invariant violation) must be refused, not deleted.
func TestAttachmentDelete_ItemTypeNullItemID_Refused(t *testing.T) {
	db := newAttachmentDeleteTestDB(t)
	h := newAttachmentHandler(db)
	user := &models.User{ID: 1, Username: "tester"}

	cases := []string{"item", ""}
	for _, et := range cases {
		label := et
		if label == "" {
			label = "empty"
		}
		t.Run(label, func(t *testing.T) {
			id := insertAttachment(t, db, et, nil, &user.ID)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodDelete, "/attachments/"+strconvI(id), nil)
			req.SetPathValue("id", strconvI(id))
			req = withUser(req, user)

			h.Delete(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("entity_type=%q: want 404, got %d (body=%s)", et, rec.Code, rec.Body.String())
			}
			var n int
			_ = db.QueryRow(`SELECT COUNT(*) FROM attachments WHERE id = ?`, id).Scan(&n)
			if n != 1 {
				t.Fatalf("item-typed row with NULL item_id was deleted")
			}
		})
	}
}

// TestAttachmentDownload_ItemTypeNullItemID_Refused exercises the matching
// nil-guard added to Download — a synthetic item row with NULL item_id must
// 404 rather than fall through to file serving.
func TestAttachmentDownload_ItemTypeNullItemID_Refused(t *testing.T) {
	db := newAttachmentDeleteTestDB(t)
	h := newAttachmentHandler(db)
	user := &models.User{ID: 1, Username: "tester"}

	id := insertAttachment(t, db, "item", nil, &user.ID)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/attachments/"+strconvI(id)+"/download", nil)
	req.SetPathValue("id", strconvI(id))
	req = withUser(req, user)

	h.Download(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("Download item/NULL: want 404, got %d", rec.Code)
	}
}

// TestAttachmentThumbnail_ItemTypeNullItemID_Refused mirrors Download for
// the thumbnail surface.
func TestAttachmentThumbnail_ItemTypeNullItemID_Refused(t *testing.T) {
	db := newAttachmentDeleteTestDB(t)
	h := newAttachmentHandler(db)
	user := &models.User{ID: 1, Username: "tester"}

	id := insertAttachment(t, db, "item", nil, &user.ID)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/attachments/"+strconvI(id)+"/thumbnail", nil)
	req.SetPathValue("id", strconvI(id))
	req = withUser(req, user)

	h.Thumbnail(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("Thumbnail item/NULL: want 404, got %d", rec.Code)
	}
}

// strconvI is a tiny local int→string to keep the strconv import out of the
// test surface where every call needs a single positive id.
func strconvI(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = digits[i%10]
		i /= 10
	}
	return string(buf[pos:])
}
