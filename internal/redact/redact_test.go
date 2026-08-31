package redact

import "testing"

func TestStringRedactsKnownSecretForms(t *testing.T) {
	in := `https://oauth2:secret@example.invalid/repo?access_token=querysecret&ref=main WS_TOKEN=ws_abc LLM_API_KEY=llmsecret AGENT_GIT_TOKEN=gitsecret Authorization: Bearer bearersecret wsrt_abc wsrc_def crw_ghi {"api_key":"k","token":"t","password":"p","authorization":"Bearer x"}`
	out := String(in)
	for _, secret := range []string{"secret@example", "access_token", "querysecret", "llmsecret", "gitsecret", "bearersecret", "wsrt_abc", "wsrc_def", "crw_ghi", `"k"`, `"t"`, `"p"`, `"Bearer x"`} {
		if contains(out, secret) {
			t.Fatalf("redacted output still contains %q: %s", secret, out)
		}
	}
	if !contains(out, "[REDACTED]") {
		t.Fatalf("redacted output missing marker: %s", out)
	}
}

func contains(s, needle string) bool {
	for i := 0; i+len(needle) <= len(s); i++ {
		if s[i:i+len(needle)] == needle {
			return true
		}
	}
	return needle == ""
}
