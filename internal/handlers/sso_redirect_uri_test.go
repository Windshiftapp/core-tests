//go:build test

package handlers

import "testing"

func TestIsValidRedirectURI(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		uri  string
		want bool
	}{
		// Valid: same-origin relative paths only.
		{"root", "/", true},
		{"dashboard", "/dashboard", true},
		{"nested path", "/settings/profile", true},
		{"item path", "/items/123", true},
		{"path with query", "/dashboard?ref=login", true},
		{"path with fragment", "/items/42#tab=comments", true},

		// Invalid: anything that can escape the origin or inject control chars.
		{"empty", "", false},
		{"protocol-relative", "//evil.com/", false},
		{"backslash-relative", `/\evil.com`, false},
		{"backslash-bypass", "/foo\\bar", false},
		{"userinfo-confusion", "/@evil.com", false},
		{"tab-injection", "/dash\tboard", false},
		{"newline-injection", "/dash\nboard", false},
		{"CR-injection", "/dash\rboard", false},
		{"native custom scheme rejected", "windshift://oauth/callback", false},
		{"native uppercased custom scheme rejected", "WINDSHIFT://oauth/callback", false},
		{"other custom scheme rejected", "fb://oauth/callback", false},
		{"https absolute rejected", "https://evil.com/", false},
		{"http absolute rejected", "http://evil.com/", false},
		{"javascript scheme rejected", "javascript:alert(1)", false},
		{"data scheme rejected", "data:text/html,foo", false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isValidRedirectURI(tc.uri); got != tc.want {
				t.Errorf("isValidRedirectURI(%q) = %v, want %v", tc.uri, got, tc.want)
			}
		})
	}
}
