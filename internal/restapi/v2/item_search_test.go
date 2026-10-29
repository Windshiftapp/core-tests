//go:build test

package v2

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"windshift/internal/middleware"
	"windshift/internal/models"
)

func TestItemSearchRateLimitUsesV2Error(t *testing.T) {
	t.Setenv("WINDSHIFT_E2E_DISABLE_RATE_LIMITS", "0")
	limiter := middleware.NewRateLimiter(0, 1, false, nil)
	t.Cleanup(limiter.Stop)
	calls := 0
	routes := []route{{Route: Route{Method: http.MethodGet, Path: "/items/search", Auth: AuthAuthenticated, Scopes: []string{"items:read"}}, handler: func(w http.ResponseWriter, r *http.Request) error {
		calls++
		w.WriteHeader(http.StatusNoContent)
		return nil
	}}}
	mux := http.NewServeMux()
	registerMount(mux, restPrefix, routes, Deps{
		Tokens:      fakeTokens{user: &models.User{ID: 1}, token: &models.APIToken{ID: 1}, allow: true, wantScopes: []string{"items:read"}},
		Concurrency: fakeConcurrency{allow: true}, CORS: NewCORS(nil, false, true), SearchAllowed: limiter.AllowRequest,
	}, false)
	for _, want := range []int{http.StatusNoContent, http.StatusTooManyRequests} {
		request := httptest.NewRequest(http.MethodGet, "/rest/api/v2/items/search?q=example", nil)
		request.Header.Set("Authorization", "Bearer crw_valid")
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != want {
			t.Fatalf("status %d want %d: %s", response.Code, want, response.Body)
		}
		if want == http.StatusTooManyRequests {
			var body errorBody
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Error.Code != "rate_limited" || body.Error.Message != "Too many search requests. Please try again later." || body.RequestID == "" {
				t.Fatalf("rate limit error = %+v", body)
			}
		}
	}
	if calls != 1 {
		t.Fatalf("search executed %d times, want once", calls)
	}
}
