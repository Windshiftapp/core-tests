//go:build test

package handlers

import "testing"

// TestIsValidNativeRedirectURI locks down the native (desktop/iOS) custom-scheme
// allowlist: only the one exact windshift://oauth/callback target is accepted,
// since the native SSO branch hands a session back to whatever handles it
// (WI-446). isValidRedirectURI (web paths) must keep rejecting all of these.
func TestIsValidNativeRedirectURI(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		uri  string
		want bool
	}{
		{"exact native callback", "windshift://oauth/callback", true},

		{"empty", "", false},
		{"trailing slash differs", "windshift://oauth/callback/", false},
		{"query appended differs", "windshift://oauth/callback?code=x", false},
		{"wrong host", "windshift://evil/callback", false},
		{"wrong path", "windshift://oauth/other", false},
		{"uppercased scheme", "WINDSHIFT://oauth/callback", false},
		{"other scheme", "fb://oauth/callback", false},
		{"relative path", "/oauth/callback", false},
		{"https absolute", "https://evil.com/oauth/callback", false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isValidNativeRedirectURI(tc.uri); got != tc.want {
				t.Errorf("isValidNativeRedirectURI(%q) = %v, want %v", tc.uri, got, tc.want)
			}
			// The web validator must never accept a native target.
			if tc.want && isValidRedirectURI(tc.uri) {
				t.Errorf("isValidRedirectURI(%q) unexpectedly accepted a native target", tc.uri)
			}
		})
	}
}
