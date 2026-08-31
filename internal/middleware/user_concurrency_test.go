package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"windshift/internal/models"
)

func reqWithUser(path string, userID int) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	return r.WithContext(context.WithValue(r.Context(), ContextKeyUser, &models.User{ID: userID}))
}

// TestUserConcurrencyLimiter_CapsPerUserAndIsolatesUsers verifies a user is
// held to `max` simultaneous requests (over-cap → 429) and that one user
// saturating their slots does not block a different user.
func TestUserConcurrencyLimiter_CapsPerUserAndIsolatesUsers(t *testing.T) {
	t.Setenv("WINDSHIFT_E2E_DISABLE_RATE_LIMITS", "0") // ensure limiter is active

	l := NewUserConcurrencyLimiter(2)
	release := make(chan struct{})
	started := make(chan struct{}, 8)
	h := l.Limit(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		started <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
	}))

	// Fill user 1's two slots with blocking requests.
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.ServeHTTP(httptest.NewRecorder(), reqWithUser("/api/items", 1))
		}()
	}
	for i := 0; i < 2; i++ {
		<-started // both slots acquired
	}

	// Third concurrent request for user 1 is rejected immediately.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, reqWithUser("/api/items", 1))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("over-cap request: want 429, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") == "" {
		t.Error("expected Retry-After header on 429")
	}

	// A different user is unaffected — its request enters the handler.
	wg.Add(1)
	go func() {
		defer wg.Done()
		h.ServeHTTP(httptest.NewRecorder(), reqWithUser("/api/items", 2))
	}()
	<-started // user 2 acquired a slot despite user 1 being saturated

	// Release everyone and confirm a fresh user-1 request now succeeds.
	close(release)
	wg.Wait()

	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, reqWithUser("/api/items", 1))
	if rec.Code != http.StatusOK {
		t.Fatalf("post-release request: want 200, got %d", rec.Code)
	}
}

// TestUserConcurrencyLimiter_Exemptions verifies streaming endpoints and
// unauthenticated requests bypass the cap even when the user is saturated.
func TestUserConcurrencyLimiter_Exemptions(t *testing.T) {
	t.Setenv("WINDSHIFT_E2E_DISABLE_RATE_LIMITS", "0")

	l := NewUserConcurrencyLimiter(1)
	if !l.acquire(1) { // saturate user 1
		t.Fatal("first acquire should succeed")
	}

	h := l.Limit(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	cases := []struct {
		name string
		req  *http.Request
		want int
	}{
		{"saturated normal path", reqWithUser("/api/items", 1), http.StatusTooManyRequests},
		{"streaming /events exempt", reqWithUser("/api/agent-runs/5/events", 1), http.StatusOK},
		{"ai stream exempt", reqWithUser("/api/ai/chat", 1), http.StatusOK},
		{"no user passthrough", httptest.NewRequest(http.MethodGet, "/api/items", nil), http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, tc.req)
			if rec.Code != tc.want {
				t.Fatalf("want %d, got %d", tc.want, rec.Code)
			}
		})
	}
}

// TestUserConcurrencyLimiter_Disabled verifies max<=0 makes Limit a passthrough.
func TestUserConcurrencyLimiter_Disabled(t *testing.T) {
	l := NewUserConcurrencyLimiter(0)
	called := false
	h := l.Limit(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, reqWithUser("/api/items", 1))
	if !called || rec.Code != http.StatusOK {
		t.Fatalf("max<=0 should disable limiting (called=%v, code=%d)", called, rec.Code)
	}
}
