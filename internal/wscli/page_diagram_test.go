package wscli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestClientPageDiagramOperations(t *testing.T) {
	t.Run("list unwraps items", func(t *testing.T) {
		client, _ := newTestPageClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Errorf("method: want GET, got %s", r.Method)
			}
			if r.URL.Path != "/rest/api/v1/workspaces/42/pages/7/diagrams" {
				t.Errorf("path: got %s", r.URL.Path)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []PageDiagram{{PageID: 7, AttachmentID: 91, Name: "Flow", Kind: "mermaid"}},
			})
		})

		got, err := client.ListPageDiagrams(42, 7)
		if err != nil {
			t.Fatalf("ListPageDiagrams: %v", err)
		}
		if len(got) != 1 || got[0].AttachmentID != 91 || got[0].Kind != "mermaid" {
			t.Fatalf("decoded diagrams: %+v", got)
		}
	})

	t.Run("get addresses attachment within page", func(t *testing.T) {
		client, _ := newTestPageClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				t.Errorf("method: want GET, got %s", r.Method)
			}
			if r.URL.Path != "/rest/api/v1/workspaces/42/pages/7/diagrams/91" {
				t.Errorf("path: got %s", r.URL.Path)
			}
			_ = json.NewEncoder(w).Encode(PageDiagram{PageID: 7, AttachmentID: 91, Name: "Flow"})
		})

		got, err := client.GetPageDiagram(42, 7, 91)
		if err != nil {
			t.Fatalf("GetPageDiagram: %v", err)
		}
		if got.PageID != 7 || got.AttachmentID != 91 {
			t.Fatalf("decoded diagram: %+v", got)
		}
	})

	t.Run("create sends placement payload and expected hash", func(t *testing.T) {
		var body PageDiagramCreateRequest
		client, _ := newTestPageClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("method: want POST, got %s", r.Method)
			}
			if r.URL.Path != "/rest/api/v1/workspaces/42/pages/7/diagrams" {
				t.Errorf("path: got %s", r.URL.Path)
			}
			readJSONBody(t, r, &body)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(PageDiagram{PageID: 7, AttachmentID: 92})
		})

		hash := "content-hash-1"
		got, err := client.CreatePageDiagram(42, 7, PageDiagramCreateRequest{
			Name:                "Flow",
			Mermaid:             "graph TD; A-->B",
			Placement:           "start",
			ExpectedContentHash: &hash,
		})
		if err != nil {
			t.Fatalf("CreatePageDiagram: %v", err)
		}
		if got.AttachmentID != 92 {
			t.Fatalf("decoded diagram: %+v", got)
		}
		if body.Name != "Flow" || body.Mermaid != "graph TD; A-->B" || body.Placement != "start" {
			t.Errorf("body: %+v", body)
		}
		if body.ExpectedContentHash == nil || *body.ExpectedContentHash != hash {
			t.Errorf("expected_content_hash: %+v", body.ExpectedContentHash)
		}
		if body.Excalidraw != nil {
			t.Errorf("excalidraw should be omitted, got %s", body.Excalidraw)
		}
	})

	t.Run("update sends replacement scene and expected hash", func(t *testing.T) {
		var body PageDiagramUpdateRequest
		client, _ := newTestPageClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPut {
				t.Errorf("method: want PUT, got %s", r.Method)
			}
			if r.URL.Path != "/rest/api/v1/workspaces/42/pages/7/diagrams/91" {
				t.Errorf("path: got %s", r.URL.Path)
			}
			readJSONBody(t, r, &body)
			_ = json.NewEncoder(w).Encode(PageDiagram{PageID: 7, AttachmentID: 93})
		})

		hash := "content-hash-2"
		scene := json.RawMessage(`{"type":"excalidraw","version":2,"elements":[],"appState":{},"files":{}}`)
		got, err := client.UpdatePageDiagram(42, 7, 91, PageDiagramUpdateRequest{
			Excalidraw:          scene,
			ExpectedContentHash: &hash,
		})
		if err != nil {
			t.Fatalf("UpdatePageDiagram: %v", err)
		}
		if got.AttachmentID != 93 {
			t.Fatalf("decoded diagram: %+v", got)
		}
		if !bytes.Equal(body.Excalidraw, scene) {
			t.Errorf("excalidraw: want %s, got %s", scene, body.Excalidraw)
		}
		if body.ExpectedContentHash == nil || *body.ExpectedContentHash != hash {
			t.Errorf("expected_content_hash: %+v", body.ExpectedContentHash)
		}
		if body.Mermaid != "" {
			t.Errorf("mermaid should be omitted, got %q", body.Mermaid)
		}
	})
}

func TestPageDiagramCommandsCoverAllOperations(t *testing.T) {
	const scene = `{"type":"excalidraw","version":2,"elements":[],"appState":{},"files":{}}`

	tests := []struct {
		name       string
		args       []string
		method     string
		path       string
		status     int
		response   any
		assertBody func(*testing.T, map[string]any)
		wantOutput []string
	}{
		{
			name:   "create mermaid at explicit placement",
			args:   []string{"page", "diagram", "create", "7", "--name", "Flow", "--mermaid", "graph TD; A-->B", "--placement", "start", "--expected-content-hash", "hash-1"},
			method: http.MethodPost,
			path:   "/rest/api/v1/workspaces/42/pages/7/diagrams",
			status: http.StatusCreated,
			response: PageDiagram{
				PageID: 7, AttachmentID: 91, Name: "Flow", Kind: "mermaid", ContentHash: "hash-2",
			},
			assertBody: func(t *testing.T, body map[string]any) {
				if body["name"] != "Flow" || body["mermaid"] != "graph TD; A-->B" {
					t.Errorf("body: %#v", body)
				}
				if body["placement"] != "start" || body["expected_content_hash"] != "hash-1" {
					t.Errorf("placement/hash body: %#v", body)
				}
				if _, ok := body["excalidraw"]; ok {
					t.Errorf("unexpected excalidraw field: %#v", body)
				}
			},
			wantOutput: []string{`"attachment_id": 91`, `"page_id": 7`, `"kind": "mermaid"`, `"content_hash": "hash-2"`},
		},
		{
			name:       "list table output",
			args:       []string{"page", "diagram", "list", "7", "-o", "table"},
			method:     http.MethodGet,
			path:       "/rest/api/v1/workspaces/42/pages/7/diagrams",
			status:     http.StatusOK,
			response:   map[string]any{"items": []PageDiagram{{PageID: 7, AttachmentID: 91, Name: "Flow", Kind: "mermaid", ContentHash: "hash-2"}}},
			wantOutput: []string{"ATTACHMENT", "PAGE", "KIND", "CONTENT HASH", "91", "7", "mermaid", "hash-2"},
		},
		{
			name:       "get",
			args:       []string{"page", "diagram", "get", "7", "91"},
			method:     http.MethodGet,
			path:       "/rest/api/v1/workspaces/42/pages/7/diagrams/91",
			status:     http.StatusOK,
			response:   PageDiagram{PageID: 7, AttachmentID: 91, Name: "Flow", Kind: "mermaid", ContentHash: "hash-2"},
			wantOutput: []string{`"attachment_id": 91`, `"name": "Flow"`},
		},
		{
			name:   "update inline Excalidraw",
			args:   []string{"page", "diagram", "update", "7", "91", "--excalidraw", scene, "--expected-content-hash", "hash-2"},
			method: http.MethodPut,
			path:   "/rest/api/v1/workspaces/42/pages/7/diagrams/91",
			status: http.StatusOK,
			response: PageDiagram{
				PageID: 7, AttachmentID: 92, Name: "Flow", Kind: "excalidraw", ContentHash: "hash-3",
			},
			assertBody: func(t *testing.T, body map[string]any) {
				if body["expected_content_hash"] != "hash-2" {
					t.Errorf("expected hash body: %#v", body)
				}
				sceneBody, ok := body["excalidraw"].(map[string]any)
				if !ok || sceneBody["type"] != "excalidraw" {
					t.Errorf("excalidraw body: %#v", body["excalidraw"])
				}
				if _, ok := body["mermaid"]; ok {
					t.Errorf("unexpected mermaid field: %#v", body)
				}
			},
			wantOutput: []string{`"attachment_id": 92`, `"kind": "excalidraw"`, `"content_hash": "hash-3"`},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var requests int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != tc.method {
					t.Errorf("method: want %s, got %s", tc.method, r.Method)
				}
				if r.URL.Path != tc.path {
					t.Errorf("path: want %s, got %s", tc.path, r.URL.Path)
				}
				if tc.assertBody != nil {
					var body map[string]any
					readJSONBody(t, r, &body)
					tc.assertBody(t, body)
				}
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(tc.response)
			}))
			t.Cleanup(server.Close)

			env := map[string]string{
				"WS_URL":       server.URL,
				"WS_TOKEN":     "ws_test_token",
				"WS_WORKSPACE": "42",
			}
			var out, errOut bytes.Buffer
			code := Run(context.Background(), tc.args, nil, &out, &errOut, env)
			if code != 0 {
				t.Fatalf("Run code %d; stderr=%s", code, errOut.String())
			}
			if requests != 1 {
				t.Fatalf("requests: want 1, got %d", requests)
			}
			for _, want := range tc.wantOutput {
				if !strings.Contains(out.String(), want) {
					t.Errorf("output missing %q:\n%s", want, out.String())
				}
			}
		})
	}
}

func TestPageDiagramCommandFromFile(t *testing.T) {
	scenePath := t.TempDir() + "/scene.json"
	if err := os.WriteFile(scenePath, []byte(`{"type":"excalidraw","version":2,"elements":[],"appState":{},"files":{}}`), 0o600); err != nil {
		t.Fatalf("write scene: %v", err)
	}

	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		readJSONBody(t, r, &gotBody)
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(PageDiagram{PageID: 7, AttachmentID: 91})
	}))
	t.Cleanup(server.Close)

	env := map[string]string{"WS_URL": server.URL, "WS_TOKEN": "token", "WS_WORKSPACE": "42"}
	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"page", "diagram", "create", "7", "--name", "File", "--from-file", scenePath}, nil, &out, &errOut, env)
	if code != 0 {
		t.Fatalf("Run code %d; stderr=%s", code, errOut.String())
	}
	if _, ok := gotBody["excalidraw"].(map[string]any); !ok {
		t.Fatalf("from-file did not send Excalidraw object: %#v", gotBody)
	}
}

func TestPageDiagramErrorsAreActionable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "invalid scene", err: &APIError{Status: 400, Code: "VALIDATION_FAILED", ErrorMessage: "invalid Excalidraw scene"}, want: "invalid diagram payload"},
		{name: "payload too large", err: &APIError{Status: 413, Code: "VALIDATION_FAILED", ErrorMessage: "diagram payload is too large"}, want: "invalid diagram payload"},
		{name: "stale page", err: &APIError{Status: 409, Code: "VALIDATION_FAILED", ErrorMessage: "page content changed"}, want: "stale Page content"},
		{name: "missing diagram", err: &APIError{Status: 404, Code: "NOT_FOUND", ErrorMessage: "not found"}, want: "diagram attachment 91 on Page 7 was not found"},
		{name: "missing page access", err: &APIError{Status: 404, Code: "NOT_FOUND", ErrorMessage: "not found"}, want: "Page 7 was not found, or you lack page access"},
		{name: "scope denied", err: &APIError{Status: 403, Code: "INSUFFICIENT_SCOPE", ErrorMessage: "missing pages:write"}, want: "token lacks the required pages API scope"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			attachmentID := 91
			if tc.name == "missing page access" {
				attachmentID = 0
			}
			got := translatePageDiagramError(tc.err, "update", 7, attachmentID)
			if !strings.Contains(got.Error(), tc.want) {
				t.Fatalf("error %q does not contain %q", got, tc.want)
			}
		})
	}
}

func TestPageDiagramInputValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "missing source", args: []string{"page", "diagram", "create", "7", "--name", "Flow"}, want: "provide one of"},
		{name: "mutually exclusive sources", args: []string{"page", "diagram", "create", "7", "--name", "Flow", "--mermaid", "graph TD; A-->B", "--excalidraw", `{}`}, want: "mutually exclusive"},
		{name: "invalid scene JSON", args: []string{"page", "diagram", "update", "7", "91", "--excalidraw", `{broken`}, want: "not valid JSON"},
		{name: "invalid page id", args: []string{"page", "diagram", "list", "nope"}, want: "invalid page id"},
	}

	env := map[string]string{"WS_URL": "http://127.0.0.1:1", "WS_TOKEN": "token", "WS_WORKSPACE": "42"}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := Run(context.Background(), tc.args, nil, &out, &errOut, env)
			if code == 0 {
				t.Fatalf("Run unexpectedly succeeded; stdout=%s", out.String())
			}
			if !strings.Contains(errOut.String(), tc.want) {
				t.Fatalf("stderr %q does not contain %q", errOut.String(), tc.want)
			}
		})
	}
}
