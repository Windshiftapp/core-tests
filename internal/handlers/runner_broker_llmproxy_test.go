package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"windshift/internal/auth"
	"windshift/internal/llm"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/sso"
	"windshift/internal/testutils"
	"windshift/internal/utils"
)

type llmBrokerFixture struct {
	handler    *RunnerBrokerHandler
	runID      int
	connection int
	token      string
	tokenID    int
	db         *testutils.TestDB
	usage      *repository.LLMUsageRepository
	manager    *llm.ConnectionManager
}

func newLLMBrokerFixture(t *testing.T, upstreamURL string, providerConfig ...string) *llmBrokerFixture {
	t.Helper()
	config := ""
	if len(providerConfig) > 0 {
		config = providerConfig[0]
	}
	return newLLMBrokerFixtureFor(t, llm.CreateConnectionRequest{
		Name: "Anthropic broker", ProviderType: "anthropic", Model: "claude-haiku-4-5-20251001",
		APIKey: "provider-secret", BaseURL: upstreamURL, ProviderConfig: config, IsEnabled: true,
	})
}

// newLLMBrokerFixtureFor builds the same fixture against an arbitrary
// connection, so a test can exercise a protocol other than Anthropic.
func newLLMBrokerFixtureFor(t *testing.T, connectionRequest llm.CreateConnectionRequest) *llmBrokerFixture {
	t.Helper()
	llm.LoadDefaultProviders()
	utils.SetAllowLocalConnections(true)
	t.Cleanup(func() { utils.SetAllowLocalConnections(true) })
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	if _, err := tdb.Exec(`INSERT INTO workspaces(id, name, key, active) VALUES (1, 'Broker', 'BRK', true)`); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}

	tokens := auth.NewTokenManager(tdb.GetDatabase(), nil)
	createdToken, err := tokens.CreateToken(1, models.APITokenCreate{Name: "run-token", IsTemporary: true})
	if err != nil {
		t.Fatalf("create run token: %v", err)
	}
	manager := llm.NewConnectionManager(tdb.GetDatabase(), sso.NewSecretEncryption("01234567890123456789012345678901"), nil)
	connection, err := manager.CreateConnection(connectionRequest)
	if err != nil {
		t.Fatalf("create LLM connection: %v", err)
	}
	runs := repository.NewAgentRunRepository(tdb.GetDatabase())
	runID, err := runs.Insert(context.Background(), &models.AgentRun{WorkspaceID: 1, Status: models.AgentRunStatusRunning})
	if err != nil {
		t.Fatalf("insert run: %v", err)
	}
	grants := &models.RunGrants{LLM: &models.LLMGrant{ConnectionID: connection.ID}}
	if err := runs.SetGrants(context.Background(), runID, createdToken.APIToken.ID, grants, time.Now()); err != nil {
		t.Fatalf("bind run grants: %v", err)
	}
	usage := repository.NewLLMUsageRepository(tdb.GetDatabase())
	handler := NewRunnerBrokerHandler(tokens, runs, nil, manager, nil)
	handler.SetUsageRepository(usage)
	return &llmBrokerFixture{
		handler: handler, runID: runID, connection: connection.ID,
		token: createdToken.Token, tokenID: createdToken.APIToken.ID, db: tdb, usage: usage,
		manager: manager,
	}
}

func (f *llmBrokerFixture) request(t *testing.T, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/llm-proxy/run/complete", bytes.NewBufferString(body))
	request.SetPathValue("run", jsonNumber(f.runID))
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Protocol-Version", "1")
	recorder := httptest.NewRecorder()
	f.handler.ProxyLLM(recorder, request)
	return recorder
}

func jsonNumber(value int) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func writeAnthropicSSE(w http.ResponseWriter, events ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, event := range events {
		_, _ = w.Write([]byte(event))
	}
}

func anthropicEvent(name, data string) string {
	return "event: " + name + "\ndata: " + data + "\n\n"
}

func TestNeutralInferenceEndpointResolvesBindingAndMetersTypedUsage(t *testing.T) {
	var upstream map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("upstream path = %q", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "provider-secret" {
			t.Fatalf("provider credential = %q", got)
		}
		if err := json.NewDecoder(r.Body).Decode(&upstream); err != nil {
			t.Fatalf("decode upstream: %v", err)
		}
		writeAnthropicSSE(w,
			anthropicEvent("message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-haiku-4-5-20251001","content":[],"stop_reason":null,"usage":{"input_tokens":10,"cache_creation_input_tokens":3,"cache_read_input_tokens":4,"output_tokens":0}}}`),
			anthropicEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`),
			anthropicEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"ready"}}`),
			anthropicEvent("content_block_stop", `{"type":"content_block_stop","index":0}`),
			anthropicEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`),
			anthropicEvent("message_stop", `{"type":"message_stop"}`),
		)
	}))
	defer server.Close()
	fixture := newLLMBrokerFixture(t, server.URL)

	recorder := fixture.request(t, fixture.token, `{"model":"attacker-selected","messages":[{"role":"user","content":"status"}],"max_tokens":999999}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	if got := string(upstream["model"]); got != `"claude-haiku-4-5-20251001"` {
		t.Fatalf("server-authoritative model = %s", got)
	}
	if got := string(upstream["max_tokens"]); got != "64000" {
		t.Fatalf("server-capped max_tokens = %s", got)
	}
	totals, err := fixture.usage.TotalsForRun(context.Background(), fixture.runID)
	if err != nil {
		t.Fatalf("usage totals: %v", err)
	}
	if totals.PromptTokens != 10 || totals.CacheReadTokens != 4 || totals.CacheWriteTokens != 3 || totals.CompletionTokens != 5 || totals.TotalTokens != 22 || totals.Calls != 1 {
		t.Fatalf("typed usage totals = %+v", totals)
	}
	if totals.CostUSD == nil {
		t.Fatal("complete Anthropic rates should produce a computed cost")
	}
}

func TestNeutralInferenceEndpointRejectsWrongAndFinishedRunTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("unauthorized request reached provider")
	}))
	defer server.Close()
	fixture := newLLMBrokerFixture(t, server.URL)
	body := `{"messages":[{"role":"user","content":"hello"}]}`
	if got := fixture.request(t, "wrong-token", body).Code; got != http.StatusUnauthorized {
		t.Fatalf("wrong token status = %d, want 401", got)
	}
	if _, err := fixture.db.Exec(`INSERT INTO workspaces(id, name, key, active) VALUES (2, 'Other', 'OTH', true)`); err != nil {
		t.Fatal(err)
	}
	otherToken, err := auth.NewTokenManager(fixture.db.GetDatabase(), nil).CreateToken(1, models.APITokenCreate{Name: "other-run", IsTemporary: true})
	if err != nil {
		t.Fatal(err)
	}
	otherRuns := repository.NewAgentRunRepository(fixture.db.GetDatabase())
	otherRunID, err := otherRuns.Insert(context.Background(), &models.AgentRun{WorkspaceID: 2, Status: models.AgentRunStatusRunning})
	if err != nil {
		t.Fatal(err)
	}
	if err := otherRuns.SetGrants(context.Background(), otherRunID, otherToken.APIToken.ID, &models.RunGrants{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if got := fixture.request(t, otherToken.Token, body).Code; got != http.StatusForbidden {
		t.Fatalf("cross-workspace run token status = %d, want 403", got)
	}
	if _, err := fixture.db.Exec(`UPDATE agent_runs SET status='succeeded' WHERE id=?`, fixture.runID); err != nil {
		t.Fatal(err)
	}
	if got := fixture.request(t, fixture.token, body).Code; got != http.StatusForbidden {
		t.Fatalf("finished run status = %d, want 403", got)
	}
}

func TestNeutralInferenceEndpointRejectsMissingAndMismatchedProtocolVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("protocol-mismatched request reached provider")
	}))
	defer server.Close()
	fixture := newLLMBrokerFixture(t, server.URL)
	body := `{"messages":[{"role":"user","content":"hello"}]}`

	// A stale agent that sends no protocol header must be rejected loudly,
	// with the broker advertising the supported version (WI-921).
	request := httptest.NewRequest(http.MethodPost, "/llm-proxy/run/complete", bytes.NewBufferString(body))
	request.SetPathValue("run", jsonNumber(fixture.runID))
	request.Header.Set("Authorization", "Bearer "+fixture.token)
	request.Header.Set("Content-Type", "application/json")
	missing := httptest.NewRecorder()
	fixture.handler.ProxyLLM(missing, request)
	if missing.Code != http.StatusUpgradeRequired {
		t.Fatalf("missing protocol header status = %d, want 426", missing.Code)
	}
	if got := missing.Header().Get("X-Protocol-Version"); got != "1" {
		t.Fatalf("advertised protocol version = %q, want %q", got, "1")
	}

	// A mismatched (future) version is rejected too, not silently accepted.
	mismatched := httptest.NewRecorder()
	misReq := httptest.NewRequest(http.MethodPost, "/llm-proxy/run/complete", bytes.NewBufferString(body))
	misReq.SetPathValue("run", jsonNumber(fixture.runID))
	misReq.Header.Set("Authorization", "Bearer "+fixture.token)
	misReq.Header.Set("Content-Type", "application/json")
	misReq.Header.Set("X-Protocol-Version", "2")
	fixture.handler.ProxyLLM(mismatched, misReq)
	if mismatched.Code != http.StatusUpgradeRequired {
		t.Fatalf("mismatched protocol header status = %d, want 426", mismatched.Code)
	}
}

func TestNeutralInferenceEndpointRejectsContinuationBindingMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("mismatched continuation reached provider")
	}))
	defer server.Close()
	fixture := newLLMBrokerFixture(t, server.URL)
	recorder := fixture.request(t, fixture.token, `{"messages":[{"role":"assistant","content":"","provider_state":{"opaque":true},"provider_binding":"sha256:other"}]}`)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestNeutralInferenceEndpointPreservesContinuationWithBindingFingerprint(t *testing.T) {
	var upstreamBodies []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var upstream map[string]any
		if err := json.NewDecoder(r.Body).Decode(&upstream); err != nil {
			t.Fatalf("decode upstream request: %v", err)
		}
		upstreamBodies = append(upstreamBodies, upstream)
		if len(upstreamBodies) == 2 {
			writeAnthropicSSE(w,
				anthropicEvent("message_start", `{"type":"message_start","message":{"id":"msg_2","type":"message","role":"assistant","model":"claude-haiku-4-5-20251001","content":[],"stop_reason":null,"usage":{"input_tokens":2,"cache_read_input_tokens":20,"output_tokens":0}}}`),
				anthropicEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`),
				anthropicEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"done"}}`),
				anthropicEvent("content_block_stop", `{"type":"content_block_stop","index":0}`),
				anthropicEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`),
				anthropicEvent("message_stop", `{"type":"message_stop"}`),
			)
			return
		}
		writeAnthropicSSE(w,
			anthropicEvent("message_start", `{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","model":"claude-haiku-4-5-20251001","content":[],"stop_reason":null,"usage":{"input_tokens":10,"cache_creation_input_tokens":20,"output_tokens":0}}}`),
			anthropicEvent("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`),
			anthropicEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"inspect"}}`),
			anthropicEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"opaque-signature"}}`),
			anthropicEvent("content_block_stop", `{"type":"content_block_stop","index":0}`),
			anthropicEvent("content_block_start", `{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"call-1","name":"read_file","input":{}}}`),
			anthropicEvent("content_block_delta", `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"README.md\"}"}}`),
			anthropicEvent("content_block_stop", `{"type":"content_block_stop","index":1}`),
			anthropicEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":5}}`),
			anthropicEvent("message_stop", `{"type":"message_stop"}`),
		)
	}))
	defer server.Close()
	fixture := newLLMBrokerFixture(t, server.URL, `{"reasoning":{"budget_tokens":2048}}`)
	recorder := fixture.request(t, fixture.token, `{"messages":[{"role":"system","content":"You are a coding agent."},{"role":"user","content":"inspect","cache_breakpoint":true}],"tools":[{"type":"function","function":{"name":"read_file","description":"read","parameters":{"type":"object"}}}]}`)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	var response llm.CompletionResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	message := response.Choices[0].Message
	if len(message.ProviderState) == 0 || message.ProviderBinding != llmBindingFingerprint(fixture.connection, "anthropic", "claude-haiku-4-5-20251001") {
		t.Fatalf("continuation state/binding = %s / %q", message.ProviderState, message.ProviderBinding)
	}
	if countJSONKey(upstreamBodies[0], "cache_control") != 3 {
		t.Fatalf("first request cache breakpoints = %d, want system + stable history + tools; body=%#v", countJSONKey(upstreamBodies[0], "cache_control"), upstreamBodies[0])
	}
	thinking, _ := upstreamBodies[0]["thinking"].(map[string]any)
	if thinking["type"] != "enabled" || thinking["budget_tokens"] != float64(2048) {
		t.Fatalf("explicit thinking config = %#v", thinking)
	}

	secondRequest := llm.CompletionRequest{Messages: []llm.Message{
		{Role: "system", Content: "You are a coding agent."},
		{Role: "user", Content: "inspect", CacheBreakpoint: true},
		message,
		{Role: "tool", Content: "file contents", ToolCallID: "call-1", Name: "read_file"},
	}, Tools: []llm.ToolDefinition{{Type: "function", Function: llm.FunctionDef{Name: "read_file", Description: "read", Parameters: json.RawMessage(`{"type":"object"}`)}}}}
	encoded, err := json.Marshal(secondRequest)
	if err != nil {
		t.Fatal(err)
	}
	second := fixture.request(t, fixture.token, string(encoded))
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d body=%s", second.Code, second.Body.String())
	}
	assertAnthropicThinkingReplay(t, upstreamBodies[1])
	third := fixture.request(t, fixture.token, `{"messages":[{"role":"system","content":"You are a coding agent."},{"role":"user","content":"inspect a different repository","cache_breakpoint":true}],"tools":[{"type":"function","function":{"name":"read_file","description":"read","parameters":{"type":"object"}}}]}`)
	if third.Code != http.StatusOK {
		t.Fatalf("changed-prefix status = %d body=%s", third.Code, third.Body.String())
	}
	if countJSONKey(upstreamBodies[2], "cache_control") != 3 {
		t.Fatalf("changed-prefix cache breakpoints = %d", countJSONKey(upstreamBodies[2], "cache_control"))
	}
	if !jsonContainsString(upstreamBodies[2], "inspect a different repository") {
		t.Fatalf("deliberate prefix change did not reach provider: %#v", upstreamBodies[2])
	}
	totals, err := fixture.usage.TotalsForRun(context.Background(), fixture.runID)
	if err != nil {
		t.Fatal(err)
	}
	if totals.CacheWriteTokens != 40 || totals.CacheReadTokens != 20 || totals.Calls != 3 {
		t.Fatalf("multi-turn cache usage totals = %+v", totals)
	}
}

func countJSONKey(value any, key string) int {
	switch typed := value.(type) {
	case map[string]any:
		count := 0
		for current, child := range typed {
			if current == key {
				count++
			}
			count += countJSONKey(child, key)
		}
		return count
	case []any:
		count := 0
		for _, child := range typed {
			count += countJSONKey(child, key)
		}
		return count
	default:
		return 0
	}
}

func jsonContainsString(value any, want string) bool {
	switch typed := value.(type) {
	case map[string]any:
		for _, child := range typed {
			if jsonContainsString(child, want) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if jsonContainsString(child, want) {
				return true
			}
		}
	case string:
		return typed == want
	}
	return false
}

func assertAnthropicThinkingReplay(t *testing.T, request map[string]any) {
	t.Helper()
	messages, ok := request["messages"].([]any)
	if !ok || len(messages) < 3 {
		t.Fatalf("upstream messages = %#v", request["messages"])
	}
	assistant, _ := messages[1].(map[string]any)
	content, _ := assistant["content"].([]any)
	if len(content) < 2 {
		t.Fatalf("assistant continuation content = %#v", content)
	}
	thinking, _ := content[0].(map[string]any)
	toolUse, _ := content[1].(map[string]any)
	if thinking["type"] != "thinking" || thinking["signature"] != "opaque-signature" || toolUse["type"] != "tool_use" {
		t.Fatalf("signed thinking/tool replay order = %#v", content)
	}
	toolResultMessage, _ := messages[2].(map[string]any)
	toolResults, _ := toolResultMessage["content"].([]any)
	if len(toolResults) != 1 {
		t.Fatalf("tool result message = %#v", toolResultMessage)
	}
	toolResult, _ := toolResults[0].(map[string]any)
	if toolResult["type"] != "tool_result" || toolResult["tool_use_id"] != "call-1" {
		t.Fatalf("tool result replay = %#v", toolResult)
	}
}
