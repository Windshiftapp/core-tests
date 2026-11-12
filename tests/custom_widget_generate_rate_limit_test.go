package tests

import (
	"net/http"
	"testing"
)

// TestCustomWidgetGenerateRateLimit pins the dedicated per-user generation
// bucket: 20 requests per hour, independent of the general AI limiter. The
// first 20 requests pass the limiter and fail later in the handler because no
// LLM is configured in tests; only the 21st is rejected with 429.
func TestCustomWidgetGenerateRateLimit(t *testing.T) {
	// The black-box harness disables rate limits by default; turn them back on
	// for this test before the server reads the environment.
	t.Setenv("WINDSHIFT_E2E_DISABLE_RATE_LIMITS", "0")
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	body := map[string]any{"prompt": "show my open items"}

	for i := 0; i < 20; i++ {
		resp := MakeAuthRequest(t, server, http.MethodPost, "/custom-widgets/generate", body)
		status := resp.StatusCode
		limit := resp.Header.Get("X-RateLimit-Limit")
		_ = resp.Body.Close()
		if status == http.StatusTooManyRequests {
			t.Fatalf("request %d was rate limited before the 20/hour burst was exhausted", i+1)
		}
		if limit != "20" {
			t.Fatalf("request %d X-RateLimit-Limit = %q, want 20", i+1, limit)
		}
	}

	resp := MakeAuthRequest(t, server, http.MethodPost, "/custom-widgets/generate", body)
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("21st request status = %d, want 429", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Fatal("429 missing Retry-After")
	}
	if got := resp.Header.Get("X-RateLimit-Limit"); got != "20" {
		t.Fatalf("429 X-RateLimit-Limit = %q, want 20", got)
	}

	var errBody struct {
		Code string `json:"code"`
	}
	DecodeJSON(t, resp, &errBody)
	if errBody.Code != "RATE_LIMITED" {
		t.Fatalf("429 code = %q, want RATE_LIMITED", errBody.Code)
	}
}
