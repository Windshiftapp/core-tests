package wscli

import "testing"

func TestParseCLIEscapes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"newline", `a\nb`, "a\nb"},
		{"two newlines", `a\n\nb`, "a\n\nb"},
		{"tab", `a\tb`, "a\tb"},
		{"carriage return", `a\rb`, "a\rb"},
		{"escaped backslash", `c:\\foo`, `c:\foo`},
		{"unknown escape kept literal", `\d+`, `\d+`},
		{"trailing lone backslash kept", `trailing\`, `trailing\`},
		{"no backslash short-circuits", "plain text", "plain text"},
		{"empty", "", ""},
		{"mixed", `# Heading\n\nBody with \\path and \tabbed`, "# Heading\n\nBody with \\path and \tabbed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseCLIEscapes(tc.in); got != tc.want {
				t.Fatalf("ParseCLIEscapes(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
