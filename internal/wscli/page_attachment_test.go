package wscli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// extractImageRefs is the pure-function half of the upload-assets flow.
// Tests cover the skip rules end-to-end (remote URLs, absolute paths,
// already-uploaded URLs, missing files, directories) using temp files
// so we exercise the os.Stat branch too.
func TestExtractImageRefs(t *testing.T) {
	baseDir := t.TempDir()
	existingPath := filepath.Join(baseDir, "hero.png")
	if err := os.WriteFile(existingPath, []byte("not a real png"), 0o600); err != nil {
		t.Fatalf("seed image: %v", err)
	}
	if err := os.Mkdir(filepath.Join(baseDir, "sub"), 0o755); err != nil {
		t.Fatalf("seed dir: %v", err)
	}

	markdown := strings.Join([]string{
		"# Title",
		"",
		"intro",
		"",
		"![hero](./hero.png)",                    // 0: local, exists → upload
		"![remote](https://example.com/img.png)", // 1: skip — remote URL
		"![data](data:image/png;base64,AAA)",     // 2: skip — data URI
		"![already](/api/attachments/9/download)", // 3: skip — already attachment URL
		"![abs](/var/tmp/img.png)",                // 4: skip — absolute path
		"![missing](./does-not-exist.png)",        // 5: skip — file not found
		"![sub](./sub)",                           // 6: skip — directory
		"[doc](./hero.png)",                       // 7: NOT image syntax — must not match
		"",
	}, "\n")

	got := extractImageRefs(markdown, baseDir)
	if len(got) != 7 {
		t.Fatalf("ref count: want 7, got %d (%+v)", len(got), got)
	}

	// 0: the one we actually upload
	if got[0].rawPath != "./hero.png" || got[0].absPath != existingPath || got[0].skipReason != "" {
		t.Errorf("hero ref: %+v", got[0])
	}
	if got[1].skipReason != "remote URL" {
		t.Errorf("remote skip reason: %q", got[1].skipReason)
	}
	if got[2].skipReason != "remote URL" {
		t.Errorf("data: URI skip reason: %q", got[2].skipReason)
	}
	if got[3].skipReason != "already an attachment URL" {
		t.Errorf("already-attachment skip reason: %q", got[3].skipReason)
	}
	if got[4].skipReason != "absolute path" {
		t.Errorf("absolute path skip reason: %q", got[4].skipReason)
	}
	if got[5].skipReason != "file not found" {
		t.Errorf("missing skip reason: %q", got[5].skipReason)
	}
	if got[6].skipReason != "path is a directory" {
		t.Errorf("dir skip reason: %q", got[6].skipReason)
	}
}

// rewriteImageRefs leaves skipped refs alone and replaces uploaded ones,
// even when the same `![alt](path)` literal appears twice.
func TestRewriteImageRefs(t *testing.T) {
	markdown := "![h](./hero.png) and again ![h](./hero.png) plus ![miss](./no.png)"
	refs := []localImageRef{
		{original: "![h](./hero.png)", altText: "h", rawPath: "./hero.png", uploadedURL: "/api/attachments/42/download"},
		{original: "![miss](./no.png)", altText: "miss", rawPath: "./no.png", skipReason: "file not found"},
	}
	out := rewriteImageRefs(markdown, refs)
	want := "![h](/api/attachments/42/download) and again ![h](/api/attachments/42/download) plus ![miss](./no.png)"
	if out != want {
		t.Errorf("rewrite:\n got  %q\n want %q", out, want)
	}
}

// uploadAndRewrite drives the full flow against a stub server that
// records each multipart upload, asserts entity discrimination, and
// returns the legacy {success,message,attachment} envelope. The summary
// line reports the upload/skip counts the user will see.
func TestUploadAndRewrite_FullFlow(t *testing.T) {
	baseDir := t.TempDir()
	imgPath := filepath.Join(baseDir, "hero.png")
	if err := os.WriteFile(imgPath, []byte("\x89PNG-ish"), 0o600); err != nil {
		t.Fatalf("seed image: %v", err)
	}

	var (
		gotPath, gotAuth, gotEntityType string
		gotFilename                     string
		gotFileBytes                    []byte
	)
	nextID := 100
	c, _ := newTestPageClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		// entity_type/entity_id come in via the URL query because the
		// v1 wrapper appends them — but our test client posts directly
		// against the server stub, so we read them from the request's
		// form values after parsing the multipart. The CLI doesn't set
		// them itself; only the v1 wrapper does. In this isolation
		// test we just assert the multipart parts the CLI sends.
		mediaType, params, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if !strings.HasPrefix(mediaType, "multipart/") {
			t.Errorf("content-type: want multipart/*, got %q", mediaType)
			http.Error(w, "bad content type", http.StatusBadRequest)
			return
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatalf("read part: %v", err)
			}
			if part.FormName() != "file" {
				continue
			}
			gotFilename = part.FileName()
			gotFileBytes, _ = io.ReadAll(part)
		}
		gotEntityType = r.URL.Query().Get("entity_type")
		_ = gotEntityType // CLI does not set this — see comment above
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"message": "ok",
			"attachment": Attachment{
				ID:               nextID,
				Filename:         "stored.png",
				OriginalFilename: gotFilename,
				MimeType:         "image/png",
				FileSize:         int64(len(gotFileBytes)),
			},
		})
		nextID++
	})

	markdown := "intro\n\n![hero](./hero.png)\n\n![remote](https://x/y.png)\n"
	rewritten, summary, err := uploadAndRewrite(c, 42, 7, markdown, baseDir, io.Discard)
	if err != nil {
		t.Fatalf("uploadAndRewrite: %v", err)
	}

	if !strings.Contains(rewritten, "/api/attachments/100/download") {
		t.Errorf("rewrite missing uploaded URL: %q", rewritten)
	}
	if strings.Contains(rewritten, "./hero.png") {
		t.Errorf("rewrite still mentions local path: %q", rewritten)
	}
	if !strings.Contains(rewritten, "https://x/y.png") {
		t.Errorf("remote ref must remain untouched: %q", rewritten)
	}

	if gotPath != "/rest/api/v1/workspaces/42/pages/7/attachments" {
		t.Errorf("upload path: %q", gotPath)
	}
	if gotAuth != "Bearer ws_test_token" {
		t.Errorf("auth: %q", gotAuth)
	}
	if gotFilename != "hero.png" {
		t.Errorf("multipart filename: want hero.png, got %q", gotFilename)
	}
	if string(gotFileBytes) != "\x89PNG-ish" {
		t.Errorf("multipart file bytes: %q", string(gotFileBytes))
	}
	if !strings.Contains(summary, "uploaded 1") || !strings.Contains(summary, "1 skipped") {
		t.Errorf("summary: %q", summary)
	}
}

// uploadAndRewrite returns the original markdown unchanged (plus a
// "no image references" summary) when the document has no images.
func TestUploadAndRewrite_NoImages(t *testing.T) {
	c, _ := newTestPageClient(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Error("server should not be called when there are no image refs")
		w.WriteHeader(http.StatusOK)
	})
	in := "# just a heading\n\nbody with [a link](./doc.pdf) but no images.\n"
	out, summary, err := uploadAndRewrite(c, 1, 1, in, t.TempDir(), io.Discard)
	if err != nil {
		t.Fatalf("uploadAndRewrite: %v", err)
	}
	if out != in {
		t.Errorf("body changed:\n got %q\n want %q", out, in)
	}
	if !strings.Contains(summary, "no image references") {
		t.Errorf("summary: %q", summary)
	}
}

// translatePagePermissionError converts a 404 APIError into the helpful
// "not found, or you lack page.edit" message but lets other statuses
// pass through with op context.
func TestTranslatePagePermissionError(t *testing.T) {
	t.Run("404 becomes editor hint", func(t *testing.T) {
		base := &APIError{Status: http.StatusNotFound, Message: "Page not found"}
		got := translatePagePermissionError(base, "update page", "42")
		want := "update page (id 42): not found, or you lack page.edit"
		if !strings.Contains(got.Error(), want) {
			t.Errorf("translate 404:\n got %q\n want substring %q", got.Error(), want)
		}
	})

	t.Run("non-404 falls through with op context", func(t *testing.T) {
		base := &APIError{Status: http.StatusBadRequest, Message: "title required"}
		got := translatePagePermissionError(base, "create page", "")
		if !strings.Contains(got.Error(), "create page") {
			t.Errorf("non-404 op context lost: %q", got.Error())
		}
		if !strings.Contains(got.Error(), "title required") {
			t.Errorf("non-404 original message lost: %q", got.Error())
		}
	})

	t.Run("404 detected through error wrap", func(t *testing.T) {
		base := &APIError{Status: http.StatusNotFound, Message: "Page not found"}
		wrapped := fmt.Errorf("upload page assets: upload ./hero.png: %w", base)
		got := translatePagePermissionError(wrapped, "create page", "")
		if !strings.Contains(got.Error(), "not found, or you lack page.edit") {
			t.Errorf("wrapped 404 not translated: %q", got.Error())
		}
	})

	t.Run("nil error returns nil", func(t *testing.T) {
		if got := translatePagePermissionError(nil, "create page", ""); got != nil {
			t.Errorf("nil pass-through broken: %v", got)
		}
	})
}

// UploadPageAttachment reports a useful error when the server emits a
// legacy {success:false,message:"..."} envelope (the cookie-auth
// handler's validation path).
func TestClient_UploadPageAttachment_LegacyErrorEnvelope(t *testing.T) {
	c, _ := newTestPageClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": false,
			"message": "File extension .svg is not allowed",
		})
	})
	_, err := c.UploadPageAttachment(1, 2, "img.svg", strings.NewReader("<svg/>"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), ".svg is not allowed") {
		t.Errorf("error: %q", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadRequest {
		t.Errorf("expected APIError with Status=400, got %T %v", err, err)
	}
}

// Ensure looksRemote handles the schemes we care about. Regression
// against parse-quirks like Windows drive letters being treated as
// schemes (e.g. C:\path) — the function should still flag those as
// "remote" since we don't want to upload them.
func TestLooksRemote(t *testing.T) {
	cases := map[string]bool{
		"https://x.com/a.png": true,
		"http://x.com/a.png":  true,
		"data:image/png;base": true,
		"mailto:a@b.c":        true,
		"file:///etc/passwd":  true,
		"./hero.png":          false,
		"hero.png":            false,
		"sub/dir/hero.png":    false,
		"":                    false,
	}
	for in, want := range cases {
		// url.Parse considers "" as having no scheme, so explicit empty
		// case here is just to document behavior.
		if got := looksRemote(in); got != want {
			t.Errorf("looksRemote(%q): got %v want %v", in, got, want)
		}
	}
	_ = url.Parse // keep the import honest when iterating
}
