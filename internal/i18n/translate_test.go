//go:build test

package i18n

import (
	"strings"
	"testing"
	"time"
)

func TestTranslateInterpolatesPlaceholders(t *testing.T) {
	got := Translate("en", "email.magicLink.body", map[string]any{"expires": "30 minutes"})
	want := "Click the button below to sign in. The link is valid for 30 minutes."
	if got != want {
		t.Fatalf("Translate() = %q, want %q", got, want)
	}
}

func TestTranslateUsesRecipientLocale(t *testing.T) {
	got := Translate("de", "email.magicLink.heading", nil)
	if want := "Bei Ihrem Portal anmelden"; got != want {
		t.Fatalf("Translate(de) = %q, want %q", got, want)
	}
	if got == Translate("en", "email.magicLink.heading", nil) {
		t.Fatal("German translation is identical to English")
	}

	polish := Translate("pl", "email.magicLink.heading", nil)
	if want := "Zaloguj się do swojego portalu"; polish != want {
		t.Fatalf("Translate(pl) = %q, want %q", polish, want)
	}
	if polish == Translate("en", "email.magicLink.heading", nil) {
		t.Fatal("Polish translation is identical to English")
	}
}

func TestTranslateUnknownLocaleFallsBackToEnglish(t *testing.T) {
	got := Translate("xx", "email.magicLink.heading", nil)
	if want := "Sign in to your portal"; got != want {
		t.Fatalf("Translate(xx) = %q, want English %q", got, want)
	}
}

func TestTranslateRegionTagFallsBackToBaseLanguage(t *testing.T) {
	// de-AT is not shipped; it should resolve to de rather than English.
	got := Translate("de-AT", "email.magicLink.heading", nil)
	if want := "Bei Ihrem Portal anmelden"; got != want {
		t.Fatalf("Translate(de-AT) = %q, want German %q", got, want)
	}
}

func TestTranslateMissingKeyReturnsKey(t *testing.T) {
	if got := Translate("en", "email.does.not.exist", nil); got != "email.does.not.exist" {
		t.Fatalf("Translate(missing) = %q, want the key itself", got)
	}
}

func TestTranslateLeavesUnknownPlaceholders(t *testing.T) {
	got := Translate("en", "email.magicLink.body", map[string]any{"other": "x"})
	if want := "Click the button below to sign in. The link is valid for {expires}."; got != want {
		t.Fatalf("Translate(unknown placeholder) = %q, want %q", got, want)
	}
}

func TestTranslateOK(t *testing.T) {
	if _, ok := TranslateOK("de", "email.magicLink.heading", nil); !ok {
		t.Fatal("known key reported no translation")
	}
	if _, ok := TranslateOK("de", "notification.doesNotExist", nil); ok {
		t.Fatal("unknown key reported a translation")
	}
	got, ok := TranslateOK("en", "email.magicLink.body", map[string]any{"expires": "1 hour"})
	if !ok || !strings.Contains(got, "1 hour") {
		t.Fatalf("TranslateOK interpolation = (%q, %v)", got, ok)
	}
}

func TestHumanizeDuration(t *testing.T) {
	tests := []struct {
		locale string
		d      time.Duration
		want   string
	}{
		{locale: "en", d: 30 * time.Minute, want: "30 minutes"},
		{locale: "en", d: time.Minute, want: "1 minute"},
		{locale: "en", d: 24 * time.Hour, want: "1 day"},
		{locale: "en", d: 7 * 24 * time.Hour, want: "7 days"},
		{locale: "de", d: 30 * time.Minute, want: "30 Minuten"},
		{locale: "de", d: 24 * time.Hour, want: "1 Tag"},
		{locale: "fr", d: 7 * 24 * time.Hour, want: "7 jours"},
		{locale: "zh-CN", d: 30 * time.Minute, want: "30 分钟"},
		{locale: "ru", d: 24 * time.Hour, want: "1 день"},
		{locale: "pl", d: 30 * time.Minute, want: "30 minut"},
		{locale: "pl", d: time.Minute, want: "1 minuta"},
		{locale: "pl", d: 24 * time.Hour, want: "1 dzień"},
		{locale: "pl", d: 7 * 24 * time.Hour, want: "7 dni"},
	}
	for _, tc := range tests {
		if got := HumanizeDuration(tc.locale, tc.d); got != tc.want {
			t.Errorf("HumanizeDuration(%q, %s) = %q, want %q", tc.locale, tc.d, got, tc.want)
		}
	}
}
