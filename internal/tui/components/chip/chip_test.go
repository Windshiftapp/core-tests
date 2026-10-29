//go:build test

package chip

import "testing"

func TestCustomStatusColorGetsContrastingText(t *testing.T) {
	if got := statusColorPair("Custom", "#fef3c7").foreground; got != "#111827" {
		t.Fatalf("light custom color should get dark text, got %s", got)
	}
	if got := statusColorPair("Custom", "#352c63").foreground; got != "#ffffff" {
		t.Fatalf("dark custom color should get light text, got %s", got)
	}
}
