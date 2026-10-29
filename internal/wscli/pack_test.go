//go:build test

package wscli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The pack CLI uploads the archive as multipart form data to the same
// /packs/apply endpoint the UI uses.

func TestPackApplyUploadsArchiveAndPrintsReport(t *testing.T) {
	var (
		gotMethod      string
		gotPath        string
		gotContentType string
		gotAuth        string
		gotWorkspace   string
		gotFileName    string
		gotBody        []byte
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		gotAuth = r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		gotWorkspace = r.FormValue("workspace_name")
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Errorf("multipart file: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer func() { _ = file.Close() }()
		gotFileName = header.Filename
		gotBody, _ = io.ReadAll(file)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"pack":"iso-starter","status":"applied"}}`)
	}))
	t.Cleanup(server.Close)

	archive := buildTinyPackArchive(t)
	dir := t.TempDir()
	packPath := filepath.Join(dir, "pack.tar.gz")
	if err := os.WriteFile(packPath, archive, 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}

	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{
		"pack", "apply", packPath, "--workspace-name", "ISO 27001",
	}, nil, &out, &errOut, map[string]string{
		"WS_URL":   server.URL,
		"WS_TOKEN": "secret-token",
	})
	if code != 0 {
		t.Fatalf("ws pack apply returned %d: %s", code, errOut.String())
	}

	if gotMethod != http.MethodPost || gotPath != "/rest/api/v2/packs/apply" {
		t.Fatalf("request target = %s %s", gotMethod, gotPath)
	}
	if !strings.HasPrefix(gotContentType, "multipart/form-data") {
		t.Fatalf("Content-Type = %q", gotContentType)
	}
	if gotAuth != "Bearer secret-token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if gotWorkspace != "ISO 27001" {
		t.Fatalf("workspace_name field = %q", gotWorkspace)
	}
	if gotFileName != "pack.tar.gz" || !bytes.Equal(gotBody, archive) {
		t.Fatalf("archive upload mismatch: filename=%q bytes=%d want %d", gotFileName, len(gotBody), len(archive))
	}
	if !strings.Contains(out.String(), `"status": "applied"`) {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestPackApplyRejectsMissingTarget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("no request expected without a target")
	}))
	t.Cleanup(server.Close)

	dir := t.TempDir()
	packPath := filepath.Join(dir, "pack.tar.gz")
	if err := os.WriteFile(packPath, buildTinyPackArchive(t), 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}

	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"pack", "apply", packPath}, nil, &out, &errOut, map[string]string{
		"WS_URL":   server.URL,
		"WS_TOKEN": "secret-token",
	})
	if code == 0 {
		t.Fatalf("apply without target must fail: stdout=%q", out.String())
	}
}

// Built-in packs are addressed by name over JSON endpoints rather than an
// archive upload.

func TestPackListPrintsBuiltinPacks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/rest/api/v2/packs" {
			t.Errorf("request target = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret-token" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[{"name":"helpdesk","version":"1.0.0","description":"Email-first support","has_content":true}]}`)
	}))
	t.Cleanup(server.Close)

	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"pack", "ls"}, nil, &out, &errOut, map[string]string{
		"WS_URL":   server.URL,
		"WS_TOKEN": "secret-token",
	})
	if code != 0 {
		t.Fatalf("ws pack ls returned %d: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "helpdesk") || !strings.Contains(out.String(), "1.0.0") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestPackApplyBuiltinPostsToNameEndpoint(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"pack":"helpdesk","status":"applied"}}`)
	}))
	t.Cleanup(server.Close)

	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{
		"pack", "apply", "--builtin", "helpdesk", "--workspace-name", "Helpdesk",
	}, nil, &out, &errOut, map[string]string{
		"WS_URL":   server.URL,
		"WS_TOKEN": "secret-token",
	})
	if code != 0 {
		t.Fatalf("ws pack apply --builtin returned %d: %s", code, errOut.String())
	}
	if gotPath != "/rest/api/v2/packs/helpdesk/apply" {
		t.Fatalf("request path = %q", gotPath)
	}
	if gotBody["workspace_name"] != "Helpdesk" {
		t.Fatalf("body = %+v", gotBody)
	}
	if !strings.Contains(out.String(), `"status": "applied"`) {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestPackVerifyBuiltinPostsToVerifyEndpoint(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"pack":"helpdesk","status":"verified"}}`)
	}))
	t.Cleanup(server.Close)

	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{
		"pack", "verify", "--builtin", "helpdesk", "--workspace-name", "Helpdesk",
	}, nil, &out, &errOut, map[string]string{
		"WS_URL":   server.URL,
		"WS_TOKEN": "secret-token",
	})
	if code != 0 {
		t.Fatalf("ws pack verify --builtin returned %d: %s", code, errOut.String())
	}
	if gotPath != "/rest/api/v2/packs/helpdesk/verify" {
		t.Fatalf("request path = %q", gotPath)
	}
	if !strings.Contains(out.String(), `"status": "verified"`) {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestPackApplyBuiltinRejectsArchiveArgument(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("no request expected")
	}))
	t.Cleanup(server.Close)

	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{
		"pack", "apply", "pack.tar.gz", "--builtin", "helpdesk", "--workspace-name", "Helpdesk",
	}, nil, &out, &errOut, map[string]string{
		"WS_URL":   server.URL,
		"WS_TOKEN": "secret-token",
	})
	if code == 0 {
		t.Fatalf("archive argument with --builtin must fail: stdout=%q", out.String())
	}
}

func TestPackApplyBuiltinRequiresTarget(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("no request expected")
	}))
	t.Cleanup(server.Close)

	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"pack", "apply", "--builtin", "helpdesk"}, nil, &out, &errOut, map[string]string{
		"WS_URL":   server.URL,
		"WS_TOKEN": "secret-token",
	})
	if code == 0 {
		t.Fatalf("apply --builtin without a target must fail: stdout=%q", out.String())
	}
}

func TestPackApplyRejectsWorkspaceIDFlag(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("no request expected: workspace-id is not a supported target")
	}))
	t.Cleanup(server.Close)

	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{
		"pack", "apply", "--builtin", "helpdesk", "--workspace-id", "4",
	}, nil, &out, &errOut, map[string]string{
		"WS_URL":   server.URL,
		"WS_TOKEN": "secret-token",
	})
	if code == 0 {
		t.Fatalf("apply --workspace-id must fail: stdout=%q", out.String())
	}
}

func buildTinyPackArchive(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	manifest := `{"schema_version":1,"kind":"windshift.pack","name":"tiny","version":"1.0.0","schema":{"configuration_set":"cs.json"}}`
	if err := tw.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0o644, Size: int64(len(manifest))}); err != nil {
		t.Fatalf("tar header: %v", err)
	}
	if _, err := tw.Write([]byte(manifest)); err != nil {
		t.Fatalf("tar write: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}
