//go:build test

package handlers

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/models"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

// validThemeLogoSVG is a well-formed, self-contained SVG used across the
// theme-logo upload and serving tests.
const validThemeLogoSVG = `<?xml version="1.0" encoding="UTF-8"?>
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><rect width="10" height="10"/></svg>`

// svgMultipartUploadRequest builds a multipart upload for an arbitrary entity
// type, filename, and payload. extra adds any additional form fields.
func svgMultipartUploadRequest(t *testing.T, entityType, filename string, content []byte, extra map[string]string) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if entityType != "" {
		if err := writer.WriteField("entity_type", entityType); err != nil {
			t.Fatalf("write entity_type field: %v", err)
		}
	}
	for key, value := range extra {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("write %s field: %v", key, err)
		}
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write file part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/attachments/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func TestValidateFileExtension_SVGOptIn(t *testing.T) {
	h := &AttachmentHandler{}
	cases := []struct {
		name     string
		filename string
		allowSVG bool
		wantErr  bool
	}{
		{name: "svg blocked by default", filename: "logo.svg", wantErr: true},
		{name: "svg allowed when opted in", filename: "logo.svg", allowSVG: true},
		{name: "uppercase svg allowed when opted in", filename: "LOGO.SVG", allowSVG: true},
		{name: "png allowed", filename: "logo.png"},
		{name: "html never allowed", filename: "logo.html", allowSVG: true, wantErr: true},
		{name: "exe never allowed", filename: "logo.exe", allowSVG: true, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := h.validateFileExtension(tc.filename, tc.allowSVG)
			if tc.wantErr && err == nil {
				t.Fatalf("validateFileExtension(%q, %v) = nil, want an error", tc.filename, tc.allowSVG)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateFileExtension(%q, %v) = %v, want nil", tc.filename, tc.allowSVG, err)
			}
		})
	}
}

func TestIsAllowedImageExtension_SVGOptIn(t *testing.T) {
	cases := []struct {
		filename string
		allowSVG bool
		want     bool
	}{
		{filename: "logo.svg", want: false},
		{filename: "logo.svg", allowSVG: true, want: true},
		{filename: "LOGO.SVG", allowSVG: true, want: true},
		{filename: "logo.png", want: true},
		{filename: "logo.exe", allowSVG: true, want: false},
	}
	for _, tc := range cases {
		if got := isAllowedImageExtension(tc.filename, tc.allowSVG); got != tc.want {
			t.Fatalf("isAllowedImageExtension(%q, %v) = %v, want %v", tc.filename, tc.allowSVG, got, tc.want)
		}
	}
}

func TestLooksLikeSVG(t *testing.T) {
	cases := []struct {
		name string
		data string
		want bool
	}{
		{name: "plain svg", data: `<svg xmlns="http://www.w3.org/2000/svg"></svg>`, want: true},
		{name: "xml declaration and doctype", data: `<?xml version="1.0"?><!DOCTYPE svg><svg></svg>`, want: true},
		{name: "no namespace svg", data: `<svg></svg>`, want: true},
		{name: "wrong namespace", data: `<svg xmlns="http://example.com/ns"></svg>`, want: false},
		{name: "html root", data: `<html><body>hi</body></html>`, want: false},
		{name: "not xml", data: `not xml at all`, want: false},
		{name: "empty", data: ``, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := looksLikeSVG([]byte(tc.data)); got != tc.want {
				t.Fatalf("looksLikeSVG(%q) = %v, want %v", tc.data, got, tc.want)
			}
		})
	}
}

// TestVerifyFileContentFromBytes_SVG pins the content sniffing contract: SVG
// is XML, so it must be recognised from its root element rather than the
// text/xml type http.DetectContentType reports, and non-SVG bytes carrying an
// .svg extension must be rejected.
func TestVerifyFileContentFromBytes_SVG(t *testing.T) {
	h := &AttachmentHandler{}

	got, err := h.verifyFileContentFromBytes([]byte(validThemeLogoSVG), "logo.svg")
	if err != nil {
		t.Fatalf("verify SVG: %v", err)
	}
	if got != "image/svg+xml" {
		t.Fatalf("detected type = %q, want image/svg+xml", got)
	}

	if _, err := h.verifyFileContentFromBytes([]byte(`<html><body>hi</body></html>`), "logo.svg"); err == nil {
		t.Fatal("HTML bytes named .svg were accepted, want a rejection")
	}

	png := tinyPNG(t)
	if got, err := h.verifyFileContentFromBytes(png, "logo.png"); err != nil || got != "image/png" {
		t.Fatalf("verify PNG = (%q, %v), want (image/png, nil)", got, err)
	}
}

// TestUploadThemeLogo_AcceptsSVGAndServesSandboxed is the end-to-end contract
// for the exception: a system admin can upload an SVG as the theme logo, the
// stored MIME type is image/svg+xml, and the public branding route serves it
// inline under a sandboxing CSP so the script-capable document cannot execute.
func TestUploadThemeLogo_AcceptsSVGAndServesSandboxed(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	permissionService, _, _ := createTestServices(t, *tdb)
	seedNegativeTestUser(t, tdb.Database, 601)
	admin := &models.User{ID: 601}
	grantSystemAdmin(t, tdb, admin.ID)

	storage := blobstore.NewLocal(t.TempDir())
	handler := &AttachmentHandler{
		db:                tdb.Database,
		storage:           storage,
		permissionService: permissionService,
		attachmentService: services.NewAttachmentServiceWithPermissions(tdb.Database, permissionService),
	}

	req := svgMultipartUploadRequest(t, "theme_logo", "logo.svg", []byte(validThemeLogoSVG), nil)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()
	handler.Upload(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("theme logo SVG upload status = %d, want 200; body=%s", rec.Code, rec.Body.String())
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

	var mimeType, entityType string
	if err := tdb.Database.QueryRow(`SELECT mime_type, entity_type FROM attachments WHERE id = ?`, response.AttachmentID).Scan(&mimeType, &entityType); err != nil {
		t.Fatalf("read attachment row: %v", err)
	}
	if mimeType != "image/svg+xml" {
		t.Fatalf("stored mime_type = %q, want image/svg+xml", mimeType)
	}
	if entityType != "theme_logo" {
		t.Fatalf("stored entity_type = %q, want theme_logo", entityType)
	}

	portal := &PortalHandler{db: tdb.Database, storage: storage}
	dlReq := httptest.NewRequest(http.MethodGet, wantURL, nil)
	dlReq.SetPathValue("id", itoa(response.AttachmentID))
	dlRec := httptest.NewRecorder()
	portal.DownloadPortalAttachment(dlRec, dlReq)

	if dlRec.Code != http.StatusOK {
		t.Fatalf("theme logo SVG download status = %d, want 200; body=%s", dlRec.Code, dlRec.Body.String())
	}
	if got := dlRec.Header().Get("Content-Type"); got != "image/svg+xml" {
		t.Fatalf("served Content-Type = %q, want image/svg+xml", got)
	}
	if got := dlRec.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "inline") {
		t.Fatalf("served Content-Disposition = %q, want an inline disposition", got)
	}
	csp := dlRec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "sandbox") || !strings.Contains(csp, "default-src 'none'") {
		t.Fatalf("served CSP = %q, want a sandboxing default-src 'none' policy", csp)
	}
	if got := dlRec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("served X-Content-Type-Options = %q, want nosniff", got)
	}
	if !strings.Contains(dlRec.Body.String(), "<svg") {
		t.Fatalf("served body did not contain the SVG document: %q", dlRec.Body.String())
	}
}

// TestUploadThemeLogo_RejectsNonSVGWithSVGExtension proves the exception does
// not weaken content sniffing: bytes that are not an SVG document are refused
// even though the filename opts into SVG.
func TestUploadThemeLogo_RejectsNonSVGWithSVGExtension(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	permissionService, _, _ := createTestServices(t, *tdb)
	seedNegativeTestUser(t, tdb.Database, 602)
	admin := &models.User{ID: 602}
	grantSystemAdmin(t, tdb, admin.ID)
	handler := newThemeLogoUploadHandler(t, tdb, permissionService)

	req := svgMultipartUploadRequest(t, "theme_logo", "logo.svg", []byte(`<html><body>not an svg</body></html>`), nil)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()
	handler.Upload(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("non-SVG content with .svg extension status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "valid SVG") {
		t.Fatalf("rejection body %q does not explain the SVG validation failure", rec.Body.String())
	}
	var count int
	if err := tdb.Database.QueryRow(`SELECT COUNT(*) FROM attachments WHERE entity_type = 'theme_logo'`).Scan(&count); err != nil {
		t.Fatalf("count theme_logo attachments: %v", err)
	}
	if count != 0 {
		t.Fatalf("rejected upload persisted %d theme_logo attachments, want 0", count)
	}
}

// TestUploadSVG_RejectedForNonThemeTargets pins the security boundary: the
// SVG exception is scoped to theme logos and every other image-only target
// still refuses the extension.
func TestUploadSVG_RejectedForNonThemeTargets(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { tdb.Close() })
	permissionService, _, _ := createTestServices(t, *tdb)
	seedNegativeTestUser(t, tdb.Database, 603)
	admin := &models.User{ID: 603}
	grantSystemAdmin(t, tdb, admin.ID)
	handler := newThemeLogoUploadHandler(t, tdb, permissionService)

	cases := []struct {
		name       string
		entityType string
		extra      map[string]string
	}{
		{name: "avatar", entityType: "avatar"},
		{name: "hub logo", entityType: "hub_logo"},
		{name: "portal logo", entityType: "portal_logo", extra: map[string]string{"entity_id": "1"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := svgMultipartUploadRequest(t, tc.entityType, "logo.svg", []byte(validThemeLogoSVG), tc.extra)
			req = withUser(req, admin)
			rec := httptest.NewRecorder()
			handler.Upload(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("%s SVG upload status = %d, want 400; body=%s", tc.entityType, rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "not allowed for security reasons") {
				t.Fatalf("%s rejection body %q does not name the extension block", tc.entityType, rec.Body.String())
			}
		})
	}
}
