//go:build test

package v2

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"windshift/internal/models"
	"windshift/internal/ratelimit"
)

func TestAuthenticatedRoutesUseSharedRateLimiter(t *testing.T) {
	t.Setenv("WINDSHIFT_E2E_DISABLE_RATE_LIMITS", "0")
	limiter := ratelimit.New(0, 1, ratelimit.ByUser())
	t.Cleanup(limiter.Stop)

	calls := 0
	routes := []route{{Route: Route{Method: http.MethodGet, Path: "/items", Auth: AuthAuthenticated, Scopes: []string{"items:read"}}, handler: func(w http.ResponseWriter, _ *http.Request) error {
		calls++
		w.WriteHeader(http.StatusNoContent)
		return nil
	}}}

	mux := http.NewServeMux()
	registerMount(mux, restPrefix, routes, Deps{
		Tokens:      fakeTokens{user: &models.User{ID: 1}, token: &models.APIToken{ID: 1}, allow: true, wantScopes: []string{"items:read"}},
		Concurrency: fakeConcurrency{allow: true},
		CORS:        NewCORS(nil, false, true),
		RateLimiter: limiter,
	}, false)

	request := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/rest/api/v2/items", nil)
		req.Header.Set("Authorization", "Bearer crw_valid")
		recorder := httptest.NewRecorder()
		mux.ServeHTTP(recorder, req)
		return recorder
	}

	if got := request(); got.Code != http.StatusNoContent {
		t.Fatalf("first request status = %d, want 204: %s", got.Code, got.Body)
	}

	denied := request()
	if denied.Code != http.StatusTooManyRequests {
		t.Fatalf("second request status = %d, want 429: %s", denied.Code, denied.Body)
	}
	if denied.Header().Get("Retry-After") == "" {
		t.Fatal("429 missing Retry-After")
	}
	if got := denied.Header().Get("X-RateLimit-Limit"); got != "1" {
		t.Fatalf("X-RateLimit-Limit = %q, want 1", got)
	}
	var body errorBody
	if err := json.Unmarshal(denied.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode 429 body: %v (%s)", err, denied.Body)
	}
	if body.Error.Code != "rate_limited" || body.RequestID == "" {
		t.Fatalf("429 body = %+v, want a v2 rate_limited error with a request id", body)
	}
	if calls != 1 {
		t.Fatalf("handler ran %d times, want once", calls)
	}
}
