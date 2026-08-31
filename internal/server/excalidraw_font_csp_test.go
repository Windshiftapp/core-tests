//go:build test

package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCreateSecurityHeaders_AllowsExcalidrawFontCDN(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := createSecurityHeaders(false, false, nil, nil, nil)(next)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(recorder, request)

	csp := recorder.Header().Get("Content-Security-Policy")
	for _, directive := range []string{
		"font-src 'self' https://esm.sh;",
		"connect-src 'self' https://esm.sh;",
	} {
		if !strings.Contains(csp, directive) {
			t.Errorf("CSP missing Excalidraw font CDN directive %q: %q", directive, csp)
		}
	}
}
