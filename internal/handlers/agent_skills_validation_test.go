//go:build test

package handlers

import (
	"strings"
	"testing"
)

func TestSkillBodyMaximumSize(t *testing.T) {
	if got := (skillBody{Name: "boundary", Body: strings.Repeat("x", maxSkillBodyLen)}).validate(); got != "" {
		t.Fatalf("body at limit rejected: %s", got)
	}
	if got := (skillBody{Name: "over", Body: strings.Repeat("x", maxSkillBodyLen+1)}).validate(); got != "body must be at most 64 KiB" {
		t.Fatalf("body over limit: %q", got)
	}
}
