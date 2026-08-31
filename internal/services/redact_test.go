package services

import "testing"

// RedactString must strip embedded URL credentials from any string
// before it reaches the agent_runs.error column or the SSE event
// stream. These cases mirror the shapes the orchestrator actually
// produces — git error output, exec failure messages, log lines that
// echo a remote URL — and the leakage paths called out in finding 2
// of the 2026-05-29 coding-agent security review.
func TestRedactString(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			"https github oauth2 token",
			"git clone https://oauth2:ghp_xxxYYYzzz@github.com/acme/widget.git failed",
			"git clone https://[REDACTED]@github.com/acme/widget.git failed",
		},
		{
			"https gitea token",
			"fatal: unable to access 'https://oauth2:gt-secret@gitea.example.com/acme/widget.git'",
			"fatal: unable to access 'https://[REDACTED]@gitea.example.com/acme/widget.git'",
		},
		{
			"http unencrypted with token",
			"http://oauth2:abc@internal-gitea/acme/widget.git",
			"http://[REDACTED]@internal-gitea/acme/widget.git",
		},
		{
			"multiple credentials in one string",
			"a=https://u1:p1@h1/ b=https://u2:p2@h2/",
			"a=https://[REDACTED]@h1/ b=https://[REDACTED]@h2/",
		},
		{
			"no credentials unchanged",
			"git clone https://github.com/acme/widget.git",
			"git clone https://github.com/acme/widget.git",
		},
		{
			"empty unchanged",
			"",
			"",
		},
		{
			"ssh url unchanged",
			"git@github.com:acme/widget.git",
			"git@github.com:acme/widget.git",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RedactString(tc.in)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
