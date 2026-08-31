package server

import "testing"

func TestIsAPIPath(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{"/api", true},
		{"/api/", true},
		{"/api/items", true},
		{"/rest", true},
		{"/rest/api/v1/items", true},
		// SPA client routes that look API-shaped must NOT be classified as API.
		{"/api-docs", false},
		{"/apifoo", false},
		{"/rest-stop", false},
		// Other client routes
		{"/", false},
		{"/workspaces/1", false},
		{"/about", false},
	}
	for _, c := range cases {
		if got := isAPIPath(c.path); got != c.want {
			t.Errorf("isAPIPath(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}
