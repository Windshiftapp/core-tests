//go:build test

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"windshift/internal/contextkeys"
	"windshift/internal/llm"
	"windshift/internal/models"
)

type stubLLMClient struct {
	responses []string
	calls     int
	requests  []llm.CompletionRequest
}

func (s *stubLLMClient) Complete(_ context.Context, request llm.CompletionRequest) (*llm.CompletionResponse, error) {
	s.requests = append(s.requests, request)
	index := s.calls
	if index >= len(s.responses) {
		index = len(s.responses) - 1
	}
	s.calls++
	return &llm.CompletionResponse{
		Choices: []llm.Choice{{Message: llm.Message{Role: "assistant", Content: s.responses[index]}}},
	}, nil
}

func (s *stubLLMClient) Health(context.Context) error { return nil }
func (s *stubLLMClient) Available() bool              { return true }

type stubLLMResolver struct {
	client llm.Client
	err    error
}

func (s stubLLMResolver) ResolveForFeatureWithOverride(string, int) (llm.Client, error) {
	return s.client, s.err
}

func validDraftJSON() string {
	return `{
		"name": "My open work",
		"description": "Items assigned to me",
		"program": "root = Card([\n  Title(\"My open work\"),\n  ItemsTable(\"assignee = currentUser() AND status_completed = false\", \"key,title,status\", \"-updated_at\", 10)\n])",
		"queries": {"item": {"ql": "assignee = currentUser() AND status_completed = false", "sort": "-updated_at", "limit": 10}}
	}`
}

func callGenerate(t *testing.T, handler *CustomWidgetGenerateHandler, body any) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/custom-widgets/generate", bytes.NewReader(payload))
	req = req.WithContext(context.WithValue(req.Context(), contextkeys.User, &models.User{ID: 7}))
	rr := httptest.NewRecorder()
	handler.GenerateCustomWidget(rr, req)
	return rr
}

func TestGenerateCustomWidgetReturnsAValidatedDraft(t *testing.T) {
	client := &stubLLMClient{responses: []string{validDraftJSON()}}
	handler := NewCustomWidgetGenerateHandler(stubLLMResolver{client: client}, llm.NewPromptStore(""))

	rr := callGenerate(t, handler, map[string]any{"prompt": "show my open work"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var response struct {
		Name     string          `json:"name"`
		Program  string          `json:"program"`
		Queries  json.RawMessage `json:"queries"`
		Attempts int             `json:"attempts"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if response.Name != "My open work" || response.Attempts != 1 {
		t.Fatalf("response = %+v, want the draft on the first attempt", response)
	}
	if !strings.HasPrefix(response.Program, "root = Card(") {
		t.Fatalf("program = %q, want the model's program", response.Program)
	}
	if !strings.Contains(string(response.Queries), "status_completed = false") {
		t.Fatalf("queries = %s, want the normalized item slot", response.Queries)
	}

	// The system prompt is the embedded one and the schema is pinned.
	if len(client.requests) != 1 {
		t.Fatalf("llm calls = %d, want 1", len(client.requests))
	}
	system := client.requests[0].Messages[0]
	if system.Role != "system" || !strings.Contains(system.Content, "OpenUI Lang") {
		t.Fatalf("system message = %+v, want the embedded custom widget prompt", system)
	}
	if string(client.requests[0].StructuredOutput.Schema) != string(llm.SchemaCustomWidget) {
		t.Fatal("structured output schema is not SchemaCustomWidget")
	}
}

func TestGenerateCustomWidgetRepairsOnceAfterValidationFailure(t *testing.T) {
	broken := `{"name": "Broken", "description": "x", "program": "root = Wibble()", "queries": {}}`
	client := &stubLLMClient{responses: []string{broken, validDraftJSON()}}
	handler := NewCustomWidgetGenerateHandler(stubLLMResolver{client: client}, llm.NewPromptStore(""))

	rr := callGenerate(t, handler, map[string]any{"prompt": "show my open work"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 after repair; body=%s", rr.Code, rr.Body.String())
	}

	var response struct {
		Attempts int `json:"attempts"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if response.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2", response.Attempts)
	}

	if len(client.requests) != 2 {
		t.Fatalf("llm calls = %d, want 2", len(client.requests))
	}
	repair := client.requests[1].Messages
	last := repair[len(repair)-1]
	if !strings.Contains(last.Content, "unknown component") {
		t.Fatalf("repair message = %q, want the validation error fed back", last.Content)
	}
}

func TestGenerateCustomWidgetRejectsTwiceInvalidDrafts(t *testing.T) {
	broken := `{"name": "Broken", "description": "x", "program": "root = Wibble()", "queries": {}}`
	client := &stubLLMClient{responses: []string{broken, broken}}
	handler := NewCustomWidgetGenerateHandler(stubLLMResolver{client: client}, llm.NewPromptStore(""))

	rr := callGenerate(t, handler, map[string]any{"prompt": "show my open work"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "could not produce a valid widget") {
		t.Fatalf("body = %s, want the validation failure", rr.Body.String())
	}
}

func TestGenerateCustomWidgetRejectsInvalidPrograms(t *testing.T) {
	tooManySlots := `{
		"name": "x", "description": "x",
		"program": "root = Card([Title(\"x\")])",
		"queries": {"item": {"ql": "a = "}}
	}`

	cases := []struct {
		name     string
		response string
	}{
		{name: "unparseable query", response: tooManySlots},
		{name: "missing name", response: `{"name": "", "description": "x", "program": "root = Card([Title(\"x\")])", "queries": {}}`},
		{name: "program without root", response: `{"name": "x", "description": "x", "program": "chart = BarChart()", "queries": {}}`},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			client := &stubLLMClient{responses: []string{test.response, test.response}}
			handler := NewCustomWidgetGenerateHandler(stubLLMResolver{client: client}, llm.NewPromptStore(""))
			rr := callGenerate(t, handler, map[string]any{"prompt": "x"})
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestGenerateCustomWidgetInputAndFeatureGating(t *testing.T) {
	client := &stubLLMClient{responses: []string{validDraftJSON()}}

	t.Run("an empty prompt is rejected without calling the model", func(t *testing.T) {
		handler := NewCustomWidgetGenerateHandler(stubLLMResolver{client: client}, llm.NewPromptStore(""))
		rr := callGenerate(t, handler, map[string]any{"prompt": "   "})
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400", rr.Code)
		}
		if client.calls != 0 {
			t.Fatalf("llm calls = %d, want none", client.calls)
		}
	})

	t.Run("a disabled feature is refused", func(t *testing.T) {
		handler := NewCustomWidgetGenerateHandler(stubLLMResolver{err: llm.ErrFeatureDisabled}, llm.NewPromptStore(""))
		rr := callGenerate(t, handler, map[string]any{"prompt": "x"})
		if rr.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body=%s", rr.Code, rr.Body.String())
		}
	})

	t.Run("an unavailable model is refused", func(t *testing.T) {
		handler := NewCustomWidgetGenerateHandler(stubLLMResolver{client: &unavailableLLMClient{}}, llm.NewPromptStore(""))
		rr := callGenerate(t, handler, map[string]any{"prompt": "x"})
		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("status = %d, want 503; body=%s", rr.Code, rr.Body.String())
		}
	})
}

type unavailableLLMClient struct{}

func (*unavailableLLMClient) Complete(context.Context, llm.CompletionRequest) (*llm.CompletionResponse, error) {
	return nil, errors.New("unavailable")
}
func (*unavailableLLMClient) Health(context.Context) error { return nil }
func (*unavailableLLMClient) Available() bool              { return false }
