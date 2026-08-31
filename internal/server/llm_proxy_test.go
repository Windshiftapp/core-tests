package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInternalLLMProxyRejectsOversizedBody(t *testing.T) {
	const secret = "shared-secret"
	handler := NewInternalLLMProxy(nil, secret)
	body := `{"content":"` + strings.Repeat("a", internalLLMProxyMaxBody) + `"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/internal/llm/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+secret)
	req.ContentLength = -1

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413 (body %q)", rec.Code, rec.Body.String())
	}
	var response struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Error != "request body too large" {
		t.Fatalf("error = %q, want %q", response.Error, "request body too large")
	}
}

func TestValidateInternalTokenRequiresBearerPrefix(t *testing.T) {
	const secret = "shared-secret"

	cases := []struct {
		name   string
		header string
		want   bool
	}{
		{name: "valid bearer", header: "Bearer " + secret, want: true},
		{name: "wrong prefix same length", header: "Token: " + secret, want: false},
		{name: "missing space", header: "Bearer" + secret, want: false},
		{name: "wrong secret", header: "Bearer nope", want: false},
		{name: "empty", header: "", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/internal/llm", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			if got := validateInternalToken(req, secret); got != tc.want {
				t.Fatalf("validateInternalToken() = %v, want %v", got, tc.want)
			}
		})
	}
}
