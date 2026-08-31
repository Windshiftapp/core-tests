package handlers

import "testing"

func TestValidateRedirectURIsAcceptsHTTPAndCustomSchemes(t *testing.T) {
	valid := [][]string{
		{"https://client.example/callback"},
		{"http://127.0.0.1:3000/callback?x=1"},
		{"com.example.app:/oauth2redirect"},
		{"myapp://callback"},
		{"urn:ietf:wg:oauth:2.0:oob"},
	}
	for _, uris := range valid {
		if err := validateRedirectURIs(uris); err != nil {
			t.Fatalf("validateRedirectURIs(%q) unexpected error: %v", uris, err)
		}
	}
}

func TestValidateRedirectURIsRejectsUnsafeSchemesAndMalformedValues(t *testing.T) {
	invalid := [][]string{
		{},
		{""},
		{" javascript:alert(1)"},
		{"javascript:alert(1)"},
		{"data:text/html,<script>alert(1)</script>"},
		{"vbscript:msgbox(1)"},
		{"file:///etc/passwd"},
		{"//evil.example/callback"},
		{"https://client.example/callback#token"},
		{"https:client.example/callback"},
		{"https://client.example/call back"},
		{"https://client.example\\@evil.example/callback"},
	}
	for _, uris := range invalid {
		if err := validateRedirectURIs(uris); err == nil {
			t.Fatalf("validateRedirectURIs(%q) succeeded; expected rejection", uris)
		}
	}
}
