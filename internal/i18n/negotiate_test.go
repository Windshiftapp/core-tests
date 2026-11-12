//go:build test

package i18n

import "testing"

func TestNegotiateAcceptLanguage(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   string
		ok     bool
	}{
		{name: "exact region wins", header: "pt-BR,pt;q=0.9", want: "pt-BR", ok: true},
		{name: "base language match", header: "de-DE,de;q=0.9,en;q=0.8", want: "de", ok: true},
		{name: "quality ordering respected", header: "fr;q=0.5,de;q=0.9", want: "de", ok: true},
		{name: "script tag maps to region locale", header: "zh-Hans-CN", want: "zh-CN", ok: true},
		{name: "korean region", header: "ko-KR,ko;q=0.9", want: "ko", ok: true},
		{name: "english region", header: "en-US,en;q=0.9", want: "en", ok: true},
		{name: "unsupported language", header: "xx-YY", want: "", ok: false},
		{name: "empty header", header: "", want: "", ok: false},
		{name: "wildcard only", header: "*", want: "", ok: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := NegotiateAcceptLanguage(tc.header)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("NegotiateAcceptLanguage(%q) = (%q, %v), want (%q, %v)", tc.header, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestIsSupported(t *testing.T) {
	tests := []struct {
		tag  string
		want bool
	}{
		{tag: "en", want: true},
		{tag: "de-AT", want: true},
		{tag: "pt_BR", want: true},
		{tag: "zh", want: true},
		{tag: "xx", want: false},
		{tag: "", want: false},
	}
	for _, tc := range tests {
		if got := IsSupported(tc.tag); got != tc.want {
			t.Errorf("IsSupported(%q) = %v, want %v", tc.tag, got, tc.want)
		}
	}
}
