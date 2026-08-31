package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"windshift/internal/utils"
)

func TestDefaultOpenAIProviderUsesResponsesContract(t *testing.T) {
	LoadDefaultProviders()

	client := NewProviderClient(ConnectionConfig{ProviderType: ProviderType("openai")})
	responses, ok := client.(*fantasyClient)
	if !ok || responses.protocol != APIContractResponses {
		t.Fatalf("default OpenAI client = %#v, want Fantasy Responses", client)
	}

	legacy := NewProviderClient(ConnectionConfig{
		ProviderType:   ProviderType("openai"),
		ProviderConfig: `{"api_contract":"chat_completions"}`,
	})
	chat, ok := legacy.(*fantasyClient)
	if !ok || chat.protocol != APIContractChatCompletions {
		t.Fatalf("forced Chat Completions client = %#v, want Fantasy Chat Completions", legacy)
	}
}

func TestResolveGenerationProtocolMatchesClientDispatch(t *testing.T) {
	LoadDefaultProviders()
	tests := []struct {
		name           string
		baseURL        string
		providerConfig string
		want           string
	}{
		{name: "catalog OpenAI defaults to Responses", want: APIContractResponses},
		{name: "custom OpenAI endpoint defaults to Chat Completions", baseURL: "https://gateway.example.com", want: APIContractChatCompletions},
		{name: "explicit Responses overrides custom endpoint", baseURL: "https://gateway.example.com", providerConfig: `{"api_contract":"responses"}`, want: APIContractResponses},
		{name: "explicit Chat Completions overrides catalog", providerConfig: `{"api_contract":"chat_completions"}`, want: APIContractChatCompletions},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := ResolveGenerationProtocol(ProviderType("openai"), test.baseURL, test.providerConfig)
			if got != test.want {
				t.Fatalf("ResolveGenerationProtocol() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestOpenAIResponsesContractMapsRequestResponseAndContinuationItems(t *testing.T) {
	LoadDefaultProviders()
	utils.SetAllowLocalConnections(true)
	t.Cleanup(func() { utils.SetAllowLocalConnections(true) })

	var requests []map[string]json.RawMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Fatalf("request path = %q, want /v1/responses", r.URL.Path)
		}
		var body map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		requests = append(requests, body)
		w.Header().Set("Content-Type", "application/json")
		if len(requests) == 1 {
			_, _ = w.Write([]byte(`{
				"id":"resp_tool","object":"response","created_at":42,"status":"completed",
				"output":[
					{"id":"rs_1","type":"reasoning","encrypted_content":"opaque-reasoning-state","summary":[]},
					{"id":"fc_1","type":"function_call","call_id":"call_1","name":"lookup_item","arguments":"{\"id\":7}"}
				],
				"usage":{"input_tokens":11,"input_tokens_details":{"cached_tokens":7},"output_tokens":5,"output_tokens_details":{"reasoning_tokens":3},"total_tokens":16}
			}`))
			return
		}
		_, _ = w.Write([]byte(`{
			"id":"resp_final","object":"response","created_at":43,"status":"completed",
			"output":[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Item seven is ready."}]}],
			"usage":{"input_tokens":20,"output_tokens":6,"total_tokens":26}
		}`))
	}))
	defer server.Close()

	client := NewProviderClient(ConnectionConfig{
		ProviderType:   ProviderType("openai"),
		Model:          "gpt-5",
		BaseURL:        server.URL,
		ProviderConfig: `{"api_contract":"responses","reasoning":{"effort":"high","summary":"detailed"},"metadata":{"source":"windshift"}}`,
		Timeout:        time.Second,
	})
	request := CompletionRequest{
		Messages:    []Message{{Role: "system", Content: "Be concise."}, {Role: "user", Content: "Look up item 7."}},
		MaxTokens:   321,
		Temperature: 0.2,
		Tools: []ToolDefinition{{
			Type: "function",
			Function: FunctionDef{
				Name:        "lookup_item",
				Description: "Looks up an item",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"id":{"type":"integer"}},"required":["id"],"additionalProperties":false}`),
				Strict:      true,
			},
		}},
		ToolChoice: map[string]interface{}{
			"type":     "function",
			"function": map[string]interface{}{"name": "lookup_item"},
		},
		StructuredOutput: &StructuredOutputConfig{
			SchemaName: "item_result",
			Schema:     json.RawMessage(`{"type":"object","properties":{"ready":{"type":"boolean"}},"required":["ready"],"additionalProperties":false}`),
			Strict:     true,
		},
	}

	first, err := client.Complete(context.Background(), request)
	if err != nil {
		t.Fatalf("first Complete() error = %v", err)
	}
	if first.Usage != (Usage{PromptTokens: 4, CompletionTokens: 5, TotalTokens: 16, CacheReadTokens: 7, ReasoningTokens: 3}) {
		t.Fatalf("first usage = %+v", first.Usage)
	}
	if len(first.Choices) != 1 || len(first.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("first choices = %+v, want one tool call", first.Choices)
	}
	call := first.Choices[0].Message.ToolCalls[0]
	if call.ID != "call_1" || call.Function.Name != "lookup_item" || call.Function.Arguments != `{"id":7}` {
		t.Fatalf("normalized tool call = %+v", call)
	}
	if len(first.Choices[0].Message.ProviderState) == 0 {
		t.Fatal("OpenAI reasoning continuation state was not preserved")
	}

	second, err := client.Complete(context.Background(), CompletionRequest{
		Messages: []Message{
			{Role: "system", Content: "Be concise."},
			{Role: "user", Content: "Look up item 7."},
			first.Choices[0].Message,
			{Role: "tool", ToolCallID: call.ID, Content: `{"ready":true}`},
		},
	})
	if err != nil {
		t.Fatalf("second Complete() error = %v", err)
	}
	if got := second.Choices[0].Message.Content; got != "Item seven is ready." {
		t.Fatalf("final content = %q", got)
	}

	if len(requests) != 2 {
		t.Fatalf("request count = %d, want 2", len(requests))
	}
	assertOpenAIResponsesFirstRequest(t, requests[0])
	assertOpenAIResponsesContinuation(t, requests[1])
}

func assertOpenAIResponsesFirstRequest(t *testing.T, body map[string]json.RawMessage) {
	t.Helper()
	if got := string(body["max_output_tokens"]); got != "321" {
		t.Fatalf("max_output_tokens = %s, want 321; body = %s", got, mustMarshalJSON(t, body))
	}
	for _, forbidden := range []string{"messages", "max_tokens", "max_completion_tokens", "response_format", "api_contract"} {
		if _, ok := body[forbidden]; ok {
			t.Fatalf("Responses request includes %q; body = %s", forbidden, mustMarshalJSON(t, body))
		}
	}
	if got := string(body["store"]); got != "false" {
		t.Fatalf("store = %s, want false", got)
	}
	var include []string
	if err := json.Unmarshal(body["include"], &include); err != nil {
		t.Fatalf("decode include: %v", err)
	}
	if len(include) != 1 || include[0] != "reasoning.encrypted_content" {
		t.Fatalf("include = %s, want reasoning.encrypted_content", body["include"])
	}
	var reasoning struct {
		Effort  string `json:"effort"`
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal(body["reasoning"], &reasoning); err != nil {
		t.Fatalf("decode reasoning: %v", err)
	}
	if reasoning.Effort != "high" || reasoning.Summary != "detailed" {
		t.Fatalf("reasoning = %+v, want effort=high summary=detailed", reasoning)
	}
	var tools []map[string]json.RawMessage
	if err := json.Unmarshal(body["tools"], &tools); err != nil {
		t.Fatalf("decode tools: %v", err)
	}
	if len(tools) != 1 || string(tools[0]["name"]) != `"lookup_item"` || string(tools[0]["strict"]) != "true" {
		t.Fatalf("Responses tools = %s", body["tools"])
	}
	var choice map[string]json.RawMessage
	if err := json.Unmarshal(body["tool_choice"], &choice); err != nil {
		t.Fatalf("decode tool_choice: %v", err)
	}
	if string(choice["type"]) != `"function"` || string(choice["name"]) != `"lookup_item"` {
		t.Fatalf("Responses tool_choice = %s", body["tool_choice"])
	}
	var textConfig struct {
		Format struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"format"`
	}
	if err := json.Unmarshal(body["text"], &textConfig); err != nil {
		t.Fatalf("decode text config: %v", err)
	}
	if textConfig.Format.Type != "json_schema" || textConfig.Format.Name != "item_result" {
		t.Fatalf("Responses text.format = %+v", textConfig.Format)
	}
	if _, ok := body["metadata"]; !ok {
		t.Fatalf("provider metadata not forwarded; body = %s", mustMarshalJSON(t, body))
	}
}

func assertOpenAIResponsesContinuation(t *testing.T, body map[string]json.RawMessage) {
	t.Helper()
	var input []map[string]json.RawMessage
	if err := json.Unmarshal(body["input"], &input); err != nil {
		t.Fatalf("decode continuation input: %v", err)
	}
	if len(input) != 5 {
		t.Fatalf("continuation input items = %d, want system, user, reasoning, function call, function output; input = %s", len(input), body["input"])
	}
	wantTypes := []string{"", "", `"reasoning"`, `"function_call"`, `"function_call_output"`}
	for i, want := range wantTypes {
		if got := string(input[i]["type"]); got != want {
			t.Fatalf("continuation input[%d].type = %s, want %s; input = %s", i, got, want, body["input"])
		}
	}
	if got := string(input[4]["call_id"]); got != `"call_1"` {
		t.Fatalf("function output call_id = %s, want call_1", got)
	}
	if got := string(input[2]["encrypted_content"]); got != `"opaque-reasoning-state"` {
		t.Fatalf("continuation reasoning encrypted_content = %s, want preserved content", got)
	}
}
