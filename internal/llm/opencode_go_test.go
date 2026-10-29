package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/utils"
)

func TestOpenCodeGoProviderCatalogEntry(t *testing.T) {
	LoadDefaultProviders()

	provider := GetProvider(ProviderType("opencode-go"))
	if provider == nil {
		t.Fatal("opencode-go provider is not registered")
	}
	if provider.Name != "OpenCode Go" {
		t.Fatalf("name = %q", provider.Name)
	}
	if provider.BaseURL != "https://opencode.ai/inference/go" {
		t.Fatalf("base_url = %q", provider.BaseURL)
	}
	if provider.ModelsEndpoint != "/v1/models" {
		t.Fatalf("models_endpoint = %q", provider.ModelsEndpoint)
	}
	if provider.SessionHeader != "x-opencode-session" {
		t.Fatalf("session_header = %q", provider.SessionHeader)
	}
	if provider.UserAgent == "" || strings.Contains(provider.UserAgent, "{version}") {
		t.Fatalf("user_agent = %q, want a resolved build-version placeholder", provider.UserAgent)
	}
	if !strings.HasPrefix(provider.UserAgent, "windshift/") {
		t.Fatalf("user_agent = %q, want an identifying windshift client", provider.UserAgent)
	}
	if provider.UsageNote == "" {
		t.Fatal("usage_note is empty; the coding-agent restriction guidance is missing")
	}
}

func TestResolveGenerationProtocolForOpenCodeGoModels(t *testing.T) {
	LoadDefaultProviders()

	tests := []struct {
		model string
		want  string
	}{
		{model: "grok-4.7", want: APIContractResponses},
		{model: "grok-4.6", want: APIContractResponses},
		{model: "gpt-6-luna", want: APIContractResponses},
		{model: "gpt-5.6-luna", want: APIContractResponses},
		{model: "muse-spark-1.3-contributor", want: APIContractResponses},
		{model: "claude-haiku-5-5", want: APIContractAnthropic},
		{model: "minimax-m3", want: APIContractAnthropic},
		{model: "minimax-m2.7", want: APIContractAnthropic},
		{model: "qwen3.8-max", want: APIContractAnthropic},
		{model: "qwen3.8-flash", want: APIContractAnthropic},
		{model: "qwen3.7-plus", want: APIContractAnthropic},
		{model: "kimi-k2.7-code", want: APIContractChatCompletions},
		{model: "glm-5.3", want: APIContractChatCompletions},
		{model: "deepseek-v4-pro", want: APIContractChatCompletions},
		{model: "longcat-2.0", want: APIContractChatCompletions},
	}
	for _, test := range tests {
		t.Run(test.model, func(t *testing.T) {
			got := ResolveGenerationProtocolForModel(ProviderType("opencode-go"), test.model, "", "")
			if got != test.want {
				t.Fatalf("ResolveGenerationProtocolForModel(%q) = %q, want %q", test.model, got, test.want)
			}
		})
	}

	if got := ResolveGenerationProtocolForModel(ProviderType("opencode-go"), "grok-4.7", "", `{"api_contract":"chat_completions"}`); got != APIContractChatCompletions {
		t.Fatalf("explicit api_contract override = %q, want chat_completions", got)
	}
}

func TestEnrichModelsAPIFormatClassifiesWithoutOverwriting(t *testing.T) {
	models := []ModelInfo{
		{ID: "grok-4.7"},
		{ID: "claude-haiku-5-5"},
		{ID: "glm-5.3"},
		{ID: "custom-model", APIFormat: APIContractResponses},
	}
	EnrichModelsAPIFormat(ProviderType("opencode-go"), models)

	want := []string{APIContractResponses, APIContractAnthropic, APIContractChatCompletions, APIContractResponses}
	for i, format := range want {
		if models[i].APIFormat != format {
			t.Fatalf("models[%d].APIFormat = %q, want %q", i, models[i].APIFormat, format)
		}
	}

	// A provider with a single protocol must not be classified.
	other := []ModelInfo{{ID: "gpt-4o"}}
	EnrichModelsAPIFormat(ProviderType("openai"), other)
	if other[0].APIFormat != "" {
		t.Fatalf("openai model APIFormat = %q, want empty", other[0].APIFormat)
	}
}

func TestProviderHeaderTransportSendsStableSessionAndUserAgent(t *testing.T) {
	utils.SetAllowLocalConnections(true)
	t.Cleanup(func() { utils.SetAllowLocalConnections(true) })

	var mu sync.Mutex
	var sessions, userAgents []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sessions = append(sessions, r.Header.Get("x-opencode-session"))
		userAgents = append(userAgents, r.Header.Get("User-Agent"))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := &http.Client{Transport: &providerHeaderTransport{
		next:          http.DefaultTransport,
		userAgent:     "windshift/test",
		sessionHeader: "x-opencode-session",
	}}
	ctx := WithSessionID(context.Background(), "ws-chat-42")
	for range 2 {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, http.NoBody)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		response, err := client.Do(request)
		if err != nil {
			t.Fatalf("do request: %v", err)
		}
		response.Body.Close()
	}

	mu.Lock()
	defer mu.Unlock()
	if len(sessions) != 2 {
		t.Fatalf("recorded %d requests", len(sessions))
	}
	for i, session := range sessions {
		if session != "ws-chat-42" {
			t.Fatalf("request %d session = %q, want ws-chat-42", i, session)
		}
	}
	for i, agent := range userAgents {
		if agent != "windshift/test" {
			t.Fatalf("request %d User-Agent = %q, want windshift/test", i, agent)
		}
	}
}

func TestProviderHeaderTransportFallsBackToStablePerRequestSession(t *testing.T) {
	utils.SetAllowLocalConnections(true)
	t.Cleanup(func() { utils.SetAllowLocalConnections(true) })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	transport := &providerHeaderTransport{next: http.DefaultTransport, sessionHeader: "x-opencode-session"}
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for range 2 {
		response, err := transport.RoundTrip(request)
		if err != nil {
			t.Fatalf("round trip: %v", err)
		}
		response.Body.Close()
	}

	if session := request.Header.Get("x-opencode-session"); session == "" {
		t.Fatal("fallback session header is missing; a provider that requires it would reject the request")
	} else if first := request.Header.Get("x-opencode-session"); first != session {
		t.Fatalf("session changed across retries: %q", session)
	}
}

// TestConnectionTestsUseTheirOwnSessionID proves the admin "test connection"
// path never reuses a user conversation id: each probe gets a fresh id, and
// both probes carry the connection-test prefix.
func TestConnectionTestsUseTheirOwnSessionID(t *testing.T) {
	LoadDefaultProviders()
	utils.SetAllowLocalConnections(true)
	t.Cleanup(func() { utils.SetAllowLocalConnections(true) })

	var mu sync.Mutex
	var sessions []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		sessions = append(sessions, r.Header.Get("x-opencode-session"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chatcmpl_1","object":"chat.completion","created":1,"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer server.Close()

	dsn := fmt.Sprintf("file:%s/opencodego.db?mode=memory&cache=shared", t.TempDir())
	db, err := database.NewSQLiteDBWithPoolSizes(dsn, 4, 1)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if _, err := db.ExecWrite(`INSERT INTO llm_connections(name, provider_type, model, base_url, is_enabled) VALUES ('Go', 'opencode-go', 'kimi-k2.7-code', ?, TRUE)`, server.URL); err != nil {
		t.Fatalf("seed connection: %v", err)
	}

	manager := NewConnectionManager(db, nil, nil)
	if err := manager.TestConnection(1); err != nil {
		t.Fatalf("TestConnection() error = %v", err)
	}
	if _, err := manager.PromptConnection(context.Background(), 1, "hi"); err != nil {
		t.Fatalf("PromptConnection() error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(sessions) != 2 {
		t.Fatalf("recorded %d requests, want 2", len(sessions))
	}
	for i, session := range sessions {
		if !strings.HasPrefix(session, "ws-connection-test-") {
			t.Fatalf("probe %d session = %q, want ws-connection-test- prefix", i, session)
		}
	}
	if sessions[0] == sessions[1] {
		t.Fatalf("both probes used the same session %q; each test must be isolated", sessions[0])
	}
}

// TestOpenCodeGoRoutesModelsToTheirEndpoints drives the real client for each
// protocol family and asserts the endpoint, headers, and model that reach the
// provider.
func TestOpenCodeGoRoutesModelsToTheirEndpoints(t *testing.T) {
	LoadDefaultProviders()
	utils.SetAllowLocalConnections(true)
	t.Cleanup(func() { utils.SetAllowLocalConnections(true) })

	var mu sync.Mutex
	type seenRequest struct {
		path      string
		session   string
		userAgent string
		model     string
	}
	var seen []seenRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&body)
		var model string
		_ = json.Unmarshal(body["model"], &model)
		mu.Lock()
		seen = append(seen, seenRequest{
			path:      r.URL.Path,
			session:   r.Header.Get("x-opencode-session"),
			userAgent: r.Header.Get("User-Agent"),
			model:     model,
		})
		mu.Unlock()

		switch r.URL.Path {
		case "/v1/responses":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"resp_1","object":"response","created_at":1,"status":"completed","output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`))
		case "/v1/messages":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_01\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"claude-haiku-5-5\",\"content\":[],\"stop_reason\":null,\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n"))
			_, _ = w.Write([]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"))
			_, _ = w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n"))
			_, _ = w.Write([]byte("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"))
			_, _ = w.Write([]byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n"))
			_, _ = w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"chatcmpl_1","object":"chat.completion","created":1,"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
		}
	}))
	defer server.Close()

	tests := []struct {
		model string
		path  string
	}{
		{model: "grok-4.7", path: "/v1/responses"},
		{model: "claude-haiku-5-5", path: "/v1/messages"},
		{model: "kimi-k2.7-code", path: "/v1/chat/completions"},
	}
	for _, test := range tests {
		t.Run(test.model, func(t *testing.T) {
			client := NewProviderClient(ConnectionConfig{
				ProviderType: ProviderType("opencode-go"),
				Model:        test.model,
				APIKey:       "go-test-key",
				BaseURL:      server.URL,
				Timeout:      5 * time.Second,
			})
			ctx := WithSessionID(context.Background(), "ws-run-99")
			if _, err := client.Complete(ctx, CompletionRequest{
				Messages:  []Message{{Role: "user", Content: "hello"}},
				MaxTokens: 16,
			}); err != nil {
				t.Fatalf("Complete() error = %v", err)
			}

			mu.Lock()
			defer mu.Unlock()
			if len(seen) != 1 {
				t.Fatalf("recorded %d requests, want 1", len(seen))
			}
			got := seen[0]
			seen = nil
			if got.path != test.path {
				t.Fatalf("path = %q, want %q", got.path, test.path)
			}
			if got.model != test.model {
				t.Fatalf("model = %q, want %q", got.model, test.model)
			}
			if got.session != "ws-run-99" {
				t.Fatalf("session = %q, want ws-run-99", got.session)
			}
			if got.userAgent == "" || !strings.HasPrefix(got.userAgent, "windshift/") {
				t.Fatalf("User-Agent = %q, want an identifying windshift client", got.userAgent)
			}
		})
	}
}
