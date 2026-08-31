//go:build test

package styles

import (
	"fmt"
	"testing"
)

func TestThemesAreTerminalColorsCatalog(t *testing.T) {
	themes := Themes()
	if len(themes) != 11 {
		t.Fatalf("expected 11 terminal themes, got %d", len(themes))
	}
	want := []string{
		"catppuccin-mocha", "catppuccin-frappe", "dracula", "nord", "gruvbox-dark", "tokyo-night",
		"kanagawa-wave", "rose-pine", "everforest-dark", "solarized-dark", "one-dark",
	}
	for i, name := range want {
		if themes[i].Name != name {
			t.Errorf("theme %d: want %q, got %q", i, name, themes[i].Name)
		}
	}
}

func TestLegacyThemesResolveToCatppuccin(t *testing.T) {
	for _, name := range []string{"windshift-dark", "void", "onyx", "system"} {
		if got := ByName(name).Name; got != DefaultTheme {
			t.Errorf("legacy theme %q resolved to %q", name, got)
		}
	}
}

func TestTerminalThemeDialogsUseBaseBackground(t *testing.T) {
	for _, theme := range Themes() {
		if fmt.Sprint(theme.Palette.BgOverlay) != fmt.Sprint(theme.Palette.BgBase) {
			t.Errorf("theme %q gives dialogs a mismatched background", theme.Name)
		}
		if fmt.Sprint(theme.Palette.BgSurfaceHovered) != fmt.Sprint(theme.Palette.Selected) {
			t.Errorf("theme %q gives focused fields a non-selection background", theme.Name)
		}
	}
}
