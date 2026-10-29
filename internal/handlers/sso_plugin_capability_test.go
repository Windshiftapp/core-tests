//go:build test && !noplugins

package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/tetratelabs/wabin/binary"
	"github.com/tetratelabs/wabin/wasm"
	"windshift/internal/plugins"
	"windshift/internal/sso"
	"windshift/internal/testutils"
)

func TestCreateSecondSSOProviderUsesLatePluginManager(t *testing.T) {
	manager := newSSOCapabilityPluginManager(t)
	defer func() {
		if err := manager.Close(); err != nil {
			t.Errorf("close plugin manager: %v", err)
		}
	}()

	if !manager.HasCapability("sso.multi-provider") {
		t.Fatal("test plugin did not expose sso.multi-provider")
	}

	tests := []struct {
		name             string
		setPluginManager bool
		wantStatus       int
		wantCode         string
		wantCount        int
	}{
		{
			name:       "denies without capability",
			wantStatus: http.StatusForbidden,
			wantCode:   "CAPABILITY_REQUIRED",
			wantCount:  1,
		},
		{
			name:             "allows after plugin manager initialization",
			setPluginManager: true,
			wantStatus:       http.StatusCreated,
			wantCount:        2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tdb := testutils.CreateTestDB(t, true)
			defer tdb.Close()

			store := sso.NewProviderStore(tdb.Database)
			first := &sso.SSOProvider{
				Slug:               "first",
				Name:               "First provider",
				ProviderType:       sso.ProviderTypeSAML,
				Enabled:            true,
				IsDefault:          true,
				SAMLIdPSSOURL:      "https://idp.example.com/sso",
				SAMLIdPCertificate: "first-certificate",
			}
			if err := store.Create(first); err != nil {
				t.Fatalf("create first provider: %v", err)
			}

			handler := NewSSOHandler(
				tdb.Database,
				nil,
				nil,
				nil,
				nil,
				"test-session-secret",
				"https://windshift.example.com",
				"",
				true,
				nil,
				false,
				nil,
			)
			if tt.setPluginManager {
				handler.SetPluginManager(manager)
			}

			body := map[string]any{
				"slug":                 "second",
				"name":                 "Second provider",
				"provider_type":        "saml",
				"enabled":              true,
				"saml_idp_sso_url":     "https://idp.example.com/sso",
				"saml_idp_certificate": "second-certificate",
			}
			payload, err := json.Marshal(body)
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}

			req := httptest.NewRequest(http.MethodPost, "/api/sso/providers", bytes.NewReader(payload))
			req.Header.Set("Content-Type", "application/json")
			recorder := httptest.NewRecorder()
			handler.CreateProvider(recorder, req)

			if recorder.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", recorder.Code, tt.wantStatus, recorder.Body.String())
			}
			if tt.wantCode != "" {
				var response struct {
					Code string `json:"code"`
				}
				if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
					t.Fatalf("decode denial response: %v", err)
				}
				if response.Code != tt.wantCode {
					t.Fatalf("code = %q, want %q", response.Code, tt.wantCode)
				}
			}

			count, err := store.Count()
			if err != nil {
				t.Fatalf("count providers: %v", err)
			}
			if count != tt.wantCount {
				t.Fatalf("provider count = %d, want %d", count, tt.wantCount)
			}
		})
	}
}

func newSSOCapabilityPluginManager(t *testing.T) *plugins.Manager {
	t.Helper()

	root := t.TempDir()
	pluginDir := filepath.Join(root, "sso-multi-provider")
	if err := os.Mkdir(pluginDir, 0o750); err != nil {
		t.Fatalf("create plugin directory: %v", err)
	}

	manifest := []byte(`{
  "name": "sso-multi-provider",
  "version": "1.0.0",
  "entryPoint": "plugin.wasm",
  "capabilities": ["sso.multi-provider"]
}`)
	if err := os.WriteFile(filepath.Join(pluginDir, "manifest.json"), manifest, 0o600); err != nil {
		t.Fatalf("write plugin manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.wasm"), minimalPluginModule(), 0o600); err != nil {
		t.Fatalf("write plugin WASM: %v", err)
	}

	manager := plugins.NewManager(root)
	if err := manager.LoadPlugins(); err != nil {
		t.Fatalf("load plugins: %v", err)
	}
	return manager
}

func minimalPluginModule() []byte {
	module := &wasm.Module{
		TypeSection:     []*wasm.FunctionType{{}},
		FunctionSection: []wasm.Index{0},
		ExportSection: []*wasm.Export{
			{Name: "init", Type: wasm.ExternTypeFunc, Index: 0},
		},
		CodeSection: []*wasm.Code{{Body: []byte{wasm.OpcodeEnd}}},
	}
	return binary.EncodeModule(module)
}
