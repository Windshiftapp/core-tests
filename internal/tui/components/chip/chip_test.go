//go:build test

package chip

import "testing"

func TestStatusColorPairUsesWebDesignTokens(t *testing.T) {
	tests := []struct {
		name, categoryColor string
		want                colorPair
	}{
		{name: "Open", categoryColor: "#6b7280", want: webStatusColors["neutral"]},
		{name: "In Progress", categoryColor: "#3b82f6", want: webStatusColors["info"]},
		{name: "Done", categoryColor: "#22c55e", want: webStatusColors["success"]},
		{name: "In Review", categoryColor: "#f59e0b", want: webStatusColors["warning"]},
		{name: "Blocked", categoryColor: "#ef4444", want: webStatusColors["danger"]},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := statusColorPair(test.name, test.categoryColor); got != test.want {
				t.Fatalf("want %#v, got %#v", test.want, got)
			}
		})
	}
}

func TestStatusColorPairFallsBackByName(t *testing.T) {
	if got := statusColorPair("Done", ""); got != webStatusColors["success"] {
		t.Fatalf("Done should use success tokens, got %#v", got)
	}
	if got := statusColorPair("In Progress", ""); got != webStatusColors["warning"] {
		t.Fatalf("In Progress should use warning tokens, got %#v", got)
	}
}

func TestCustomStatusColorGetsContrastingText(t *testing.T) {
	if got := statusColorPair("Custom", "#fef3c7").foreground; got != "#111827" {
		t.Fatalf("light custom color should get dark text, got %s", got)
	}
	if got := statusColorPair("Custom", "#352c63").foreground; got != "#ffffff" {
		t.Fatalf("dark custom color should get light text, got %s", got)
	}
}
