//go:build test

package wscli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// The update endpoint merges custom_field_values per field server-side, so
// `task edit --custom-field` sends exactly the requested fields and must not
// fetch the item's current values first.
func TestTaskEditCustomFieldSendsOnlyRequestedFields(t *testing.T) {
	var mu sync.Mutex
	var sawGet bool
	var patchBody string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/rest/api/v2/items/55":
			sawGet = true
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":{"id":55,"title":"Item 55","custom_field_values":{"11":"keep me","22":42}}}`)
		case r.Method == http.MethodPatch && r.URL.Path == "/rest/api/v2/items/55":
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Errorf("read patch body: %v", err)
			}
			patchBody = string(body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":{"id":55,"title":"Item 55","custom_field_values":{"11":"keep me","22":42,"33":"High"}}}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"task", "edit", "55", "--custom-field", "33=High"}, nil, &out, &errOut, map[string]string{
		"WS_URL":   server.URL,
		"WS_TOKEN": "secret-token",
	})
	if code != 0 {
		t.Fatalf("task edit exited %d, stderr: %s", code, errOut.String())
	}
	if sawGet {
		t.Fatalf("task edit fetched the item even though custom fields merge server-side")
	}

	var payload struct {
		CustomFieldValues map[string]any `json:"custom_field_values"`
	}
	if err := json.Unmarshal([]byte(patchBody), &payload); err != nil {
		t.Fatalf("decode patch body %q: %v", patchBody, err)
	}
	want := map[string]any{"33": "High"}
	if len(payload.CustomFieldValues) != len(want) {
		t.Fatalf("patch custom_field_values = %v, want %v", payload.CustomFieldValues, want)
	}
	for key, wantValue := range want {
		if got, ok := payload.CustomFieldValues[key]; !ok || got != wantValue {
			t.Fatalf("patch custom_field_values[%q] = %v, want %v (full payload: %v)", key, got, wantValue, payload.CustomFieldValues)
		}
	}
}
