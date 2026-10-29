//go:build test

package handlers

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

// newThemeLogoHandler wires a theme handler against a production-schema test
// database. The auditor is nil because these tests assert HTTP contracts, not
// audit rows.
func newThemeLogoHandler(t *testing.T) (*ThemeHandler, *testutils.TestDB) {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	handler := NewThemeHandler(
		services.NewThemeService(repository.NewThemeRepository(tdb.Database)),
		nil,
	)
	return handler, tdb
}

// createThemeRequest builds a POST /themes request carrying the given light and
// dark logo URLs.
func createThemeRequest(t *testing.T, logoURL, logoURLDark string) *http.Request {
	t.Helper()
	payload := map[string]any{
		"name":                       "Acme",
		"description":                "Company theme",
		"nav_background_color_light": "#ffffff",
		"nav_text_color_light":       "#374151",
		"nav_background_color_dark":  "#1f2937",
		"nav_text_color_dark":        "#f3f4f6",
		"logo_url":                   logoURL,
		"logo_url_dark":              logoURLDark,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return httptest.NewRequest(http.MethodPost, "/api/themes", bytes.NewReader(body))
}

func storedThemeLogoURL(t *testing.T, tdb *testutils.TestDB, themeID int) string {
	t.Helper()
	return storedThemeLogoColumn(t, tdb, themeID, "logo_url")
}

func storedThemeLogoDarkURL(t *testing.T, tdb *testutils.TestDB, themeID int) string {
	t.Helper()
	return storedThemeLogoColumn(t, tdb, themeID, "logo_url_dark")
}

// storedThemeLogoColumn reads a theme logo column directly so tests prove the
// value survived persistence, not just the response encoding.
func storedThemeLogoColumn(t *testing.T, tdb *testutils.TestDB, themeID int, column string) string {
	t.Helper()
	var stored string
	if err := tdb.Database.QueryRow(`SELECT COALESCE(`+column+`, '') FROM themes WHERE id = ?`, themeID).Scan(&stored); err != nil {
		t.Fatalf("read stored %s: %v", column, err)
	}
	return stored
}

func TestCreateTheme_LogoURLContract(t *testing.T) {
	cases := []struct {
		name       string
		logoURL    string
		wantStatus int
		wantStored string
	}{
		{name: "same-origin path accepted", logoURL: "/api/portal-assets/12", wantStatus: http.StatusCreated, wantStored: "/api/portal-assets/12"},
		{name: "https URL accepted", logoURL: "https://cdn.example.com/acme.png", wantStatus: http.StatusCreated, wantStored: "https://cdn.example.com/acme.png"},
		{name: "empty logo URL accepted", logoURL: "", wantStatus: http.StatusCreated, wantStored: ""},
		{name: "javascript scheme rejected", logoURL: "javascript:alert(1)", wantStatus: http.StatusBadRequest},
		{name: "protocol-relative URL rejected", logoURL: "//evil.example.com/logo.png", wantStatus: http.StatusBadRequest},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler, tdb := newThemeLogoHandler(t)

			rec := httptest.NewRecorder()
			handler.CreateTheme(rec, createThemeRequest(t, tc.logoURL, ""))

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus != http.StatusCreated {
				// Rejections must name the offending field so admins can fix them.
				if !strings.Contains(rec.Body.String(), "Logo URL") {
					t.Fatalf("rejection body %q does not mention the logo URL field", rec.Body.String())
				}
				return
			}

			var created models.Theme
			if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
				t.Fatalf("decode created theme: %v", err)
			}
			if created.LogoURL != tc.wantStored {
				t.Fatalf("created logo_url = %q, want %q", created.LogoURL, tc.wantStored)
			}
			if stored := storedThemeLogoURL(t, tdb, created.ID); stored != tc.wantStored {
				t.Fatalf("persisted logo_url = %q, want %q", stored, tc.wantStored)
			}
		})
	}
}

func TestCreateTheme_DarkLogoURLContract(t *testing.T) {
	cases := []struct {
		name       string
		logoURL    string
		wantStatus int
		wantStored string
	}{
		{name: "same-origin path accepted", logoURL: "/api/portal-assets/9", wantStatus: http.StatusCreated, wantStored: "/api/portal-assets/9"},
		{name: "https URL accepted", logoURL: "https://cdn.example.com/acme-dark.png", wantStatus: http.StatusCreated, wantStored: "https://cdn.example.com/acme-dark.png"},
		{name: "empty dark logo accepted", logoURL: "", wantStatus: http.StatusCreated, wantStored: ""},
		{name: "javascript scheme rejected", logoURL: "javascript:alert(1)", wantStatus: http.StatusBadRequest},
		{name: "protocol-relative URL rejected", logoURL: "//evil.example.com/logo.png", wantStatus: http.StatusBadRequest},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handler, tdb := newThemeLogoHandler(t)

			rec := httptest.NewRecorder()
			handler.CreateTheme(rec, createThemeRequest(t, "/api/portal-assets/1", tc.logoURL))

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus != http.StatusCreated {
				if !strings.Contains(rec.Body.String(), "Dark mode logo URL") {
					t.Fatalf("rejection body %q does not name the dark mode logo URL field", rec.Body.String())
				}
				return
			}

			var created models.Theme
			if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
				t.Fatalf("decode created theme: %v", err)
			}
			if created.LogoURL != "/api/portal-assets/1" {
				t.Fatalf("created logo_url = %q, want light URL unchanged", created.LogoURL)
			}
			if created.LogoURLDark != tc.wantStored {
				t.Fatalf("created logo_url_dark = %q, want %q", created.LogoURLDark, tc.wantStored)
			}
			if stored := storedThemeLogoDarkURL(t, tdb, created.ID); stored != tc.wantStored {
				t.Fatalf("persisted logo_url_dark = %q, want %q", stored, tc.wantStored)
			}
		})
	}
}

func TestUpdateTheme_DarkLogoURLSetAndClear(t *testing.T) {
	handler, tdb := newThemeLogoHandler(t)

	rec := httptest.NewRecorder()
	handler.CreateTheme(rec, createThemeRequest(t, "/api/portal-assets/1", ""))
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed create status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var created models.Theme
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode created theme: %v", err)
	}

	update := map[string]any{
		"name":                       "Acme",
		"description":                "Company theme",
		"nav_background_color_light": "#ffffff",
		"nav_text_color_light":       "#374151",
		"nav_background_color_dark":  "#1f2937",
		"nav_text_color_dark":        "#f3f4f6",
		"logo_url":                   "/api/portal-assets/1",
		"logo_url_dark":              "https://cdn.example.com/acme-dark.png",
		"is_active":                  false,
	}
	body, err := json.Marshal(update)
	if err != nil {
		t.Fatalf("marshal update: %v", err)
	}

	rec = httptest.NewRecorder()
	updateReq := httptest.NewRequest(http.MethodPut, "/api/themes/"+strconvI(created.ID), bytes.NewReader(body))
	updateReq.SetPathValue("id", strconvI(created.ID))
	handler.UpdateTheme(rec, updateReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if stored := storedThemeLogoDarkURL(t, tdb, created.ID); stored != "https://cdn.example.com/acme-dark.png" {
		t.Fatalf("logo_url_dark after update = %q, want dark logo URL", stored)
	}
	if stored := storedThemeLogoURL(t, tdb, created.ID); stored != "/api/portal-assets/1" {
		t.Fatalf("logo_url after dark-logo update = %q, want light URL unchanged", stored)
	}

	update["logo_url_dark"] = ""
	body, err = json.Marshal(update)
	if err != nil {
		t.Fatalf("marshal clear update: %v", err)
	}
	rec = httptest.NewRecorder()
	clearReq := httptest.NewRequest(http.MethodPut, "/api/themes/"+strconvI(created.ID), bytes.NewReader(body))
	clearReq.SetPathValue("id", strconvI(created.ID))
	handler.UpdateTheme(rec, clearReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("clearing update status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if stored := storedThemeLogoDarkURL(t, tdb, created.ID); stored != "" {
		t.Fatalf("logo_url_dark after clearing update = %q, want empty", stored)
	}
}

func TestUpdateTheme_LogoURLSetAndClear(t *testing.T) {
	handler, tdb := newThemeLogoHandler(t)

	rec := httptest.NewRecorder()
	handler.CreateTheme(rec, createThemeRequest(t, "/api/portal-assets/12", ""))
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed create status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var created models.Theme
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode created theme: %v", err)
	}

	update := map[string]any{
		"name":                       "Acme",
		"description":                "Company theme",
		"nav_background_color_light": "#ffffff",
		"nav_text_color_light":       "#374151",
		"nav_background_color_dark":  "#1f2937",
		"nav_text_color_dark":        "#f3f4f6",
		"logo_url":                   "https://cdn.example.com/rebrand.png",
		"is_active":                  false,
	}
	body, err := json.Marshal(update)
	if err != nil {
		t.Fatalf("marshal update: %v", err)
	}

	rec = httptest.NewRecorder()
	updateReq := httptest.NewRequest(http.MethodPut, "/api/themes/"+strconvI(created.ID), bytes.NewReader(body))
	updateReq.SetPathValue("id", strconvI(created.ID))
	handler.UpdateTheme(rec, updateReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if stored := storedThemeLogoURL(t, tdb, created.ID); stored != "https://cdn.example.com/rebrand.png" {
		t.Fatalf("logo_url after update = %q, want rebrand URL", stored)
	}

	update["logo_url"] = ""
	body, err = json.Marshal(update)
	if err != nil {
		t.Fatalf("marshal clear update: %v", err)
	}
	rec = httptest.NewRecorder()
	clearReq := httptest.NewRequest(http.MethodPut, "/api/themes/"+strconvI(created.ID), bytes.NewReader(body))
	clearReq.SetPathValue("id", strconvI(created.ID))
	handler.UpdateTheme(rec, clearReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("clearing update status = %d; body=%s", rec.Code, rec.Body.String())
	}
	if stored := storedThemeLogoURL(t, tdb, created.ID); stored != "" {
		t.Fatalf("logo_url after clearing update = %q, want empty", stored)
	}
}

// tinyPNG renders a valid 1x1 PNG so the upload pipeline's content sniffing
// and thumbnail generation exercise real image data.
func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 0xff, A: 0xff})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// themeLogoUploadRequest builds a multipart upload for the theme_logo entity
// type from the given authenticated caller.
func themeLogoUploadRequest(t *testing.T, pngBytes []byte) (*http.Request, *multipart.Writer) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("entity_type", "theme_logo"); err != nil {
		t.Fatalf("write entity_type field: %v", err)
	}
	fileWriter, err := writer.CreateFormFile("file", "logo.png")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fileWriter.Write(pngBytes); err != nil {
		t.Fatalf("write file part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/attachments/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req, writer
}

// newThemeLogoUploadHandler builds a fully wired attachment handler on the
// production schema. The attachment path points at a fresh temp dir, which
// also makes IsEnabled() true (no attachment_settings rows exist, so the
// handler's enabled-by-default settings apply).
func newThemeLogoUploadHandler(t *testing.T, tdb *testutils.TestDB, permissionService *services.PermissionService) *AttachmentHandler {
	t.Helper()
	return &AttachmentHandler{
		db:                tdb.Database,
		storage:           blobstore.NewLocal(t.TempDir()),
		permissionService: permissionService,
		attachmentService: services.NewAttachmentServiceWithPermissions(tdb.Database, permissionService),
	}
}

func TestUploadThemeLogo_AdminAllowedNonAdminForbidden(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	permissionService, _, _ := createTestServices(t, *tdb)
	seedNegativeTestUser(t, tdb.Database, 501)
	seedNegativeTestUser(t, tdb.Database, 502)
	admin := &models.User{ID: 501}
	plain := &models.User{ID: 502}
	grantSystemAdmin(t, tdb, admin.ID)
	handler := newThemeLogoUploadHandler(t, tdb, permissionService)

	t.Run("system admin upload succeeds", func(t *testing.T) {
		req, _ := themeLogoUploadRequest(t, tinyPNG(t))
		req = withUser(req, admin)
		rec := httptest.NewRecorder()

		handler.Upload(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("admin upload status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		var response struct {
			Success      bool   `json:"success"`
			LogoURL      string `json:"logo_url"`
			AttachmentID int    `json:"attachment_id"`
		}
		if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
			t.Fatalf("decode upload response: %v", err)
		}
		if !response.Success {
			t.Fatalf("upload response success = false; body=%s", rec.Body.String())
		}
		wantURL := "/api/portal-assets/" + itoa(response.AttachmentID)
		if response.LogoURL != wantURL {
			t.Fatalf("logo_url = %q, want %q", response.LogoURL, wantURL)
		}
		var entityType, category string
		if err := tdb.Database.QueryRow(`SELECT entity_type, COALESCE(category, '') FROM attachments WHERE id = ?`, response.AttachmentID).Scan(&entityType, &category); err != nil {
			t.Fatalf("read attachment row: %v", err)
		}
		if entityType != "theme_logo" || category != "theme_logo" {
			t.Fatalf("attachment entity_type=%q category=%q, want theme_logo for both", entityType, category)
		}
	})

	t.Run("non-admin forbidden", func(t *testing.T) {
		req, _ := themeLogoUploadRequest(t, tinyPNG(t))
		req = withUser(req, plain)
		rec := httptest.NewRecorder()

		handler.Upload(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("non-admin upload status = %d, want 403; body=%s", rec.Code, rec.Body.String())
		}
		var count int
		if err := tdb.Database.QueryRow(`SELECT COUNT(*) FROM attachments WHERE entity_type = 'theme_logo' AND uploaded_by = ?`, plain.ID).Scan(&count); err != nil {
			t.Fatalf("count theme_logo attachments: %v", err)
		}
		if count != 0 {
			t.Fatalf("non-admin upload persisted %d theme_logo attachments, want 0", count)
		}
	})
}

// TestPortalAssets_ServesThemeLogo pins the public download contract for
// theme logos: an anonymous caller can fetch an attachment only when both
// entity_type and category identify it as a theme logo.
func TestPortalAssets_ServesThemeLogo(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	attachmentPath := t.TempDir()
	pngPath := attachmentPath + "/logo.png"
	if err := os.WriteFile(pngPath, tinyPNG(t), 0o600); err != nil {
		t.Fatalf("write png fixture: %v", err)
	}
	attachmentID := testutils.InsertID(t, tdb.Database, `
		INSERT INTO attachments (entity_type, category, filename, original_filename, file_path, mime_type, file_size, thumbnail_path)
		VALUES ('theme_logo', 'theme_logo', 'logo.png', 'logo.png', ?, 'image/png', 70, '')
	`, pngPath)
	handler := &PortalHandler{db: tdb.Database, storage: blobstore.NewLocal(attachmentPath)}

	req := httptest.NewRequest(http.MethodGet, "/api/portal-assets/"+strconvI(attachmentID), nil)
	req.SetPathValue("id", strconvI(attachmentID))
	rec := httptest.NewRecorder()
	handler.DownloadPortalAttachment(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("theme logo download status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	// An item attachment must not become publicly fetchable by tagging.
	mislabeledID := testutils.InsertID(t, tdb.Database, `
		INSERT INTO attachments (item_id, entity_type, category, filename, original_filename, file_path, mime_type, file_size, thumbnail_path)
		VALUES (1, 'item', 'theme_logo', 'f.png', 'f.png', ?, 'image/png', 70, '')
	`, pngPath)
	req = httptest.NewRequest(http.MethodGet, "/api/portal-assets/"+strconvI(mislabeledID), nil)
	req.SetPathValue("id", strconvI(mislabeledID))
	rec = httptest.NewRecorder()
	handler.DownloadPortalAttachment(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("mislabeled item attachment status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}
