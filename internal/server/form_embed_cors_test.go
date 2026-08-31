//go:build test

package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"windshift/internal/middleware"
)

func TestFormEmbedCORSCredentialBoundary(t *testing.T) {
	tests := []struct {
		name           string
		origin         string
		requestHost    string
		wantCookie     bool
		wantCSRFExempt bool
	}{
		{
			name:        "same origin keeps authenticated session",
			origin:      "https://windshift.example",
			requestHost: "windshift.example",
			wantCookie:  true,
		},
		{
			name:           "cross site embed is anonymous",
			origin:         "https://embed.example",
			requestHost:    "windshift.example",
			wantCookie:     false,
			wantCSRFExempt: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var gotCookie string
			var gotCSRFExempt bool
			next := http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				gotCookie = request.Header.Get("Cookie")
				gotCSRFExempt, _ = request.Context().Value(middleware.ContextKeyCSRFExempt).(bool)
				w.WriteHeader(http.StatusNoContent)
			})
			fallback := func(handler http.Handler) http.Handler { return handler }
			handler := createFormEmbedCORSMiddleware(
				"https://windshift.example,https://embed.example",
				[]string{"https://windshift.example"},
				fallback,
			)(next)

			request := httptest.NewRequest(http.MethodPost, "https://"+test.requestHost+"/api/forms/support/submit", nil)
			request.Host = test.requestHost
			request.Header.Set("Origin", test.origin)
			request.Header.Set("Cookie", "session=secret")
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request.WithContext(context.Background()))

			if recorder.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204; body=%s", recorder.Code, recorder.Body.String())
			}
			if (gotCookie != "") != test.wantCookie {
				t.Fatalf("cookie retained = %v, want %v", gotCookie != "", test.wantCookie)
			}
			if gotCSRFExempt != test.wantCSRFExempt {
				t.Fatalf("CSRF exempt = %v, want %v", gotCSRFExempt, test.wantCSRFExempt)
			}
		})
	}
}
