//go:build !noplugins

package plugins

import (
	"net/http"
	"testing"
)

func TestForbiddenPluginResponseHeader(t *testing.T) {
	cases := map[string]bool{
		"Set-Cookie":                   true,
		"set-cookie":                   true,
		"Set-Cookie2":                  true,
		"Access-Control-Allow-Origin":  true,
		"access-control-allow-headers": true,
		"Content-Length":               true,
		"Transfer-Encoding":            true,
		"Strict-Transport-Security":    true,
		"Content-Security-Policy":      true,
		"X-Frame-Options":              true,
		"Clear-Site-Data":              true,
		"Connection":                   true,
		"Upgrade":                      true,
		"Content-Disposition":          false,
		"Location":                     false,
		"Cache-Control":                false,
		"ETag":                         false,
		"Content-Type":                 false,
		"X-Custom":                     false,
	}
	for header, want := range cases {
		if got := forbiddenPluginResponseHeader(header); got != want {
			t.Errorf("forbiddenPluginResponseHeader(%q) = %v, want %v", header, got, want)
		}
	}
}

func TestPluginRouterDropsForbiddenResponseHeaders(t *testing.T) {
	source := &fakePluginSource{
		plugin: routeAuthPlugin(),
		response: &HTTPResponse{
			StatusCode: http.StatusOK,
			Headers: map[string]string{
				"Set-Cookie":                  "session=evil",
				"Access-Control-Allow-Origin": "*",
				"Content-Length":              "3",
				"Content-Disposition":         "attachment; filename=report.csv",
				"Cache-Control":               "no-store",
				"X-Custom":                    "kept",
				"Content-Type":                "text/plain",
			},
			Body: "ok",
		},
	}
	mux := newTestRouter(t, source, false)

	rec := doRequest(mux, "GET", "/api/plugins/demo/public", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (%s)", rec.Code, rec.Body.String())
	}

	for _, header := range []string{"Set-Cookie", "Access-Control-Allow-Origin", "Content-Length"} {
		if got := rec.Header().Get(header); got != "" {
			t.Errorf("%s = %q, want it dropped", header, got)
		}
	}
	for header, want := range map[string]string{
		"Content-Disposition": "attachment; filename=report.csv",
		"Cache-Control":       "no-store",
		"X-Custom":            "kept",
		"Content-Type":        "text/plain",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}
