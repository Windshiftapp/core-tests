package tests

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"

	_ "github.com/lib/pq"
	_ "modernc.org/sqlite"
)

// Framework pack apply (WI-1336): a fixture pack binding a configuration-set
// template (schema), a workspace bundle (content), and plugin references is
// applied through the production HTTP API. Covers the success path,
// missing/old plugin failures, and manifest violations. Installing a real
// plugin requires a WASM binary, so the installed-plugin fixture seeds
// plugin_registry directly — the documented corruption-fixture exception.

type packFixtureArchive struct {
	archive []byte
}

func buildConfigSetTemplateForPack(t *testing.T, server *TestServer) (templateJSON []byte, configSetID int) {
	t.Helper()
	typeResp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/item-types", map[string]interface{}{
		"name": "Pack Control", "hierarchy_level": 1,
	})
	defer typeResp.Body.Close()
	AssertStatusCode(t, typeResp, http.StatusCreated)
	var createdType struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	DecodeJSON(t, typeResp, &createdType)

	csResp := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v2/configuration-sets", map[string]interface{}{
		"name": "Pack CS",
		"item_type_configs": []map[string]interface{}{
			{"item_type_id": createdType.Data.ID},
		},
	})
	defer csResp.Body.Close()
	AssertStatusCode(t, csResp, http.StatusCreated)
	var createdCS struct {
		Data struct {
			ConfigurationSet struct {
				ID int `json:"id"`
			} `json:"configuration_set"`
		} `json:"data"`
	}
	DecodeJSON(t, csResp, &createdCS)

	template := exportV2Template(t, server, createdCS.Data.ConfigurationSet.ID)
	return template, createdCS.Data.ConfigurationSet.ID
}

type packParts struct {
	templateJSON []byte
	bundleJSON   []byte
}

func buildPackParts(t *testing.T, server *TestServer) packParts {
	t.Helper()
	templateJSON, _ := buildConfigSetTemplateForPack(t, server)

	bundle := map[string]interface{}{
		"schema_version": 1,
		"kind":           "windshift.workspace-bundle",
		"payload": map[string]interface{}{
			"pages": []map[string]interface{}{
				{"ref": "page-1", "title": "Control Catalog", "content": "# Controls\n\nAll of them."},
			},
			"items": []map[string]interface{}{
				{"ref": "item-1", "title": "Control A-1", "item_type_name": "Pack Control"},
			},
		},
	}
	bundleJSON, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("marshal bundle: %v", err)
	}
	return packParts{templateJSON: templateJSON, bundleJSON: bundleJSON}
}

// buildPackArchive composes an archive from shared parts. plugins nil = no
// plugin references.
func buildPackArchiveFrom(t *testing.T, parts packParts, plugins []map[string]interface{}) []byte {
	t.Helper()

	manifest := map[string]interface{}{
		"schema_version": 1,
		"kind":           "windshift.pack",
		"name":           "iso-starter",
		"version":        "1.0.0",
		"schema":         map[string]interface{}{"configuration_set": "configuration-set.json"},
		"content":        map[string]interface{}{"workspace_bundle": "workspace-bundle.json"},
		"conformance":    map[string]interface{}{"configuration_set": "configuration-set.json"},
	}
	if plugins != nil {
		manifest["plugins"] = plugins
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}

	return tarGz(t, map[string][]byte{
		"manifest.json":          manifestJSON,
		"configuration-set.json": parts.templateJSON,
		"workspace-bundle.json":  parts.bundleJSON,
	})
}

func tarGz(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		header := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(content))}
		if err := tw.WriteHeader(header); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write(content); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

func makeMultipartBearerRequest(t *testing.T, server *TestServer, endpoint string, fields map[string]string, filename string, fileBytes []byte) *http.Response {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, value := range fields {
		if err := writer.WriteField(name, value); err != nil {
			t.Fatalf("write multipart field: %v", err)
		}
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := part.Write(fileBytes); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, server.BaseURL+endpoint, &body)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+server.BearerToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("multipart request: %v", err)
	}
	return resp
}

func applyPack(t *testing.T, server *TestServer, endpoint string, archive []byte, fields map[string]string) (status string, stages, plugins []map[string]any, conformance map[string]any) {
	t.Helper()
	resp := makeMultipartBearerRequest(t, server, endpoint, fields, "pack.tar.gz", archive)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	{
		raw, _ := io.ReadAll(resp.Body)
		t.Logf("PACK REPORT: %s", raw)
		var decoded struct {
			Data struct {
				Status      string           `json:"status"`
				Stages      []map[string]any `json:"stages"`
				Plugins     []map[string]any `json:"plugins"`
				Conformance map[string]any   `json:"conformance"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("decode report: %v", err)
		}
		return decoded.Data.Status, decoded.Data.Stages, decoded.Data.Plugins, decoded.Data.Conformance
	}
	var decoded struct {
		Data struct {
			Status      string           `json:"status"`
			Stages      []map[string]any `json:"stages"`
			Plugins     []map[string]any `json:"plugins"`
			Conformance map[string]any   `json:"conformance"`
		} `json:"data"`
	}
	DecodeJSON(t, resp, &decoded)
	return decoded.Data.Status, decoded.Data.Stages, decoded.Data.Plugins, decoded.Data.Conformance
}

func stageByName(t *testing.T, stages []map[string]any, name string) map[string]any {
	t.Helper()
	for _, s := range stages {
		if s["name"] == name {
			return s
		}
	}
	t.Fatalf("stage %q missing from report: %+v", name, stages)
	return nil
}

func TestPackApply(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	parts := buildPackParts(t, server)
	archive := buildPackArchiveFrom(t, parts, nil)

	t.Run("apply_success", func(t *testing.T) {
		status, stages, pluginChecks, conformance := applyPack(t, server, "/rest/api/v2/packs/apply", archive,
			map[string]string{"workspace_name": "ISO Starter"})
		if status != "applied" {
			t.Fatalf("apply status = %q (stages: %+v conformance: %+v plugins: %+v)", status, stages, conformance, pluginChecks)
		}
		// Schema is "ok" on a fresh import and "skipped" when the instance
		// already has a same-named configuration set to adopt.
		schemaStage := stageByName(t, stages, "schema")
		if schemaStage["status"] != "ok" && schemaStage["status"] != "skipped" {
			t.Fatalf("schema stage = %+v, want ok or skipped", schemaStage)
		}
		if conformance == nil || conformance["conformant"] != true {
			t.Fatalf("conformance report = %+v", conformance)
		}
	})

	t.Run("workspace_content_and_schema_present", func(t *testing.T) {
		wsID := findWorkspaceByName(t, server, "ISO Starter")
		if wsID == 0 {
			t.Fatalf("applied workspace not found")
		}
		bundle, _ := exportWorkspaceBundle(t, server, wsID)
		if len(bundle.Payload.Pages) != 1 || bundle.Payload.Pages[0].Title != "Control Catalog" {
			t.Fatalf("applied pages = %+v", bundle.Payload.Pages)
		}
		if len(bundle.Payload.Items) != 1 || bundle.Payload.Items[0].Title != "Control A-1" {
			t.Fatalf("applied items = %+v", bundle.Payload.Items)
		}
		if bundle.ConfigurationSet == nil || bundle.ConfigurationSet.Payload.ConfigurationSet.Name != "Pack CS" {
			t.Fatalf("configuration set not attached: %+v", bundle.ConfigurationSet)
		}
	})

	t.Run("missing_plugin_fails_before_anything_is_created", func(t *testing.T) {
		failing := buildPackArchiveFrom(t, parts, []map[string]interface{}{
			{"name": "ghost-pack-plugin", "min_version": "1.0.0"},
		})
		status, stages, plugins, _ := applyPack(t, server, "/rest/api/v2/packs/apply", failing,
			map[string]string{"workspace_name": "Should Not Exist"})
		if status != "failed" {
			t.Fatalf("apply status = %q, want failed", status)
		}
		if stageByName(t, stages, "plugins")["status"] != "failed" {
			t.Fatalf("plugin stage = %+v", stageByName(t, stages, "plugins"))
		}
		if len(plugins) != 1 || plugins[0]["installed"] != false || plugins[0]["name"] != "ghost-pack-plugin" {
			t.Fatalf("plugin checks = %+v", plugins)
		}
		for _, s := range stages {
			if s["name"] == "workspace" {
				t.Fatalf("workspace stage must not run when plugins fail: %+v", stages)
			}
		}

		if findWorkspaceByName(t, server, "Should Not Exist") != 0 {
			t.Fatalf("failed apply created the workspace anyway")
		}
	})

	t.Run("installed_plugin_below_min_version_fails", func(t *testing.T) {
		// Fixture exception: plugin installation requires a real WASM binary,
		// so the registry row is seeded directly (SQLite and PostgreSQL both
		// expose plugin_registry with the same columns).
		db := openTestDB(t, server)
		defer db.Close()
		if _, err := db.Exec(`INSERT INTO plugin_registry (name, version, path, enabled) VALUES ('old-pack-plugin', '0.9.0', '/tmp/old.wasm', true)`); err != nil {
			t.Fatalf("seed plugin registry: %v", err)
		}

		failing := buildPackArchiveFrom(t, parts, []map[string]interface{}{
			{"name": "old-pack-plugin", "min_version": "1.0.0"},
		})
		status, _, plugins, _ := applyPack(t, server, "/rest/api/v2/packs/apply", failing,
			map[string]string{"workspace_name": "Also Should Not Exist"})
		if status != "failed" {
			t.Fatalf("apply status = %q, want failed", status)
		}
		if len(plugins) != 1 {
			t.Fatalf("plugin checks = %+v", plugins)
		}
		if plugins[0]["found_version"] != "0.9.0" || plugins[0]["min_version"] != "1.0.0" || plugins[0]["ok"] != false {
			t.Fatalf("plugin check must name required and found versions: %+v", plugins[0])
		}
	})

	t.Run("verify_reports_without_applying", func(t *testing.T) {
		status, stages, _, _ := applyPack(t, server, "/rest/api/v2/packs/verify", archive,
			map[string]string{"workspace_name": "ISO Starter"})
		if status != "verified" {
			t.Fatalf("verify status = %q", status)
		}
		for _, s := range stages {
			if s["name"] == "workspace" {
				t.Fatalf("verify must not resolve apply stages: %+v", stages)
			}
		}
	})
}

func TestPackApplyManifestViolations(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	baseManifest := map[string]interface{}{
		"schema_version": 1,
		"kind":           "windshift.pack",
		"name":           "broken-pack",
		"version":        "1.0.0",
		"schema":         map[string]interface{}{"configuration_set": "configuration-set.json"},
	}

	cases := []struct {
		name     string
		manifest map[string]interface{}
		files    map[string][]byte
		wantErr  string
	}{
		{
			name: "unknown_kind",
			manifest: func() map[string]interface{} {
				m := cloneMap(baseManifest)
				m["kind"] = "windshift.not-a-pack"
				return m
			}(),
			files:   map[string][]byte{"configuration-set.json": []byte(`{}`)},
			wantErr: `unsupported kind "windshift.not-a-pack"`,
		},
		{
			name:     "missing_version",
			manifest: func() map[string]interface{} { m := cloneMap(baseManifest); delete(m, "version"); return m }(),
			files:    map[string][]byte{"configuration-set.json": []byte(`{}`)},
			wantErr:  "not a semver",
		},
		{
			name:     "dangling_file_reference",
			manifest: baseManifest,
			files:    map[string][]byte{},
			wantErr:  `schema file "configuration-set.json" is not in the archive`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manifestJSON, err := json.Marshal(tc.manifest)
			if err != nil {
				t.Fatalf("marshal manifest: %v", err)
			}
			files := map[string][]byte{"manifest.json": manifestJSON}
			for name, content := range tc.files {
				files[name] = content
			}
			archive := tarGz(t, files)
			resp := makeMultipartBearerRequest(t, server, "/rest/api/v2/packs/apply",
				map[string]string{"workspace_name": "Violations WS"}, "broken.tar.gz", archive)
			defer resp.Body.Close()
			AssertStatusCode(t, resp, http.StatusBadRequest)
			var decoded struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			DecodeJSON(t, resp, &decoded)
			if decoded.Error.Code != "invalid_pack" {
				t.Fatalf("error code = %q", decoded.Error.Code)
			}
			if !strings.Contains(decoded.Error.Message, tc.wantErr) {
				t.Fatalf("error message %q does not mention %q", decoded.Error.Message, tc.wantErr)
			}
		})
	}
}

// openTestDB opens the fixture database directly. Used only for the
// plugin_registry seeding fixture, where no HTTP path can install a plugin
// without a real WASM binary (documented corruption-fixture exception).
func openTestDB(t *testing.T, server *TestServer) *sql.DB {
	t.Helper()
	driver := "sqlite"
	if GetDBType() == "postgres" {
		driver = "postgres"
	}
	db, err := sql.Open(driver, server.DBPath)
	if err != nil {
		t.Fatalf("open fixture db: %v", err)
	}
	return db
}

// findWorkspaceByName lists workspaces through the v1 directory and returns
// the matching workspace ID (0 when absent).
func findWorkspaceByName(t *testing.T, server *TestServer, name string) int {
	t.Helper()
	resp := MakeAuthRequest(t, server, http.MethodGet, "/workspaces", nil)
	defer resp.Body.Close()
	AssertStatusCode(t, resp, http.StatusOK)
	var workspaces []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	DecodeJSON(t, resp, &workspaces)
	for _, ws := range workspaces {
		if ws.Name == name {
			return ws.ID
		}
	}
	return 0
}

func cloneMap(src map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}
