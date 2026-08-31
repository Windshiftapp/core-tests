//go:build test

package routes

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"windshift/internal/router"
)

type rejectingPublicBoardLimiter struct {
	calls int
}

func (l *rejectingPublicBoardLimiter) Limit(http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		l.calls++
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	})
}

func TestRegisterPublicBoardRoutesAppliesSharedRateLimiter(t *testing.T) {
	mux := http.NewServeMux()
	limiter := &rejectingPublicBoardLimiter{}
	RegisterPublicBoardRoutes(&Deps{
		API:                router.NewRouteGroup(mux, "/api"),
		PublicBoardLimiter: limiter,
	})

	paths := []string{
		"/api/public/board/shared-board",
		"/api/public/board/shared-board/items/WI-12",
		"/api/public/board/shared-board/attachments/34/download",
	}
	for _, path := range paths {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		mux.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusTooManyRequests {
			t.Fatalf("GET %s status = %d, want 429", path, recorder.Code)
		}
	}
	if limiter.calls != len(paths) {
		t.Fatalf("limiter calls = %d, want %d", limiter.calls, len(paths))
	}
}
