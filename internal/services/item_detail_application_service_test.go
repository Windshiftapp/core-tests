//go:build test

package services

import (
	"testing"

	"windshift/internal/repository"
)

// A nil resolution (personal workspace, missing workspace, or nothing
// configured) must degrade to the absolute fallback screen, while concrete IDs
// pass through unchanged.
func TestEffectiveScreenIDOrFallback(t *testing.T) {
	if got := effectiveScreenIDOrFallback(nil, false); got != repository.FallbackScreenID {
		t.Fatalf("nil resolution edit screen = %d, want %d", got, repository.FallbackScreenID)
	}
	if got := effectiveScreenIDOrFallback(nil, true); got != repository.FallbackScreenID {
		t.Fatalf("nil resolution view screen = %d, want %d", got, repository.FallbackScreenID)
	}

	edit, view := 5, 6
	resolved := &repository.EffectiveConfig{EditScreenID: &edit, ViewScreenID: &view}
	if got := effectiveScreenIDOrFallback(resolved, false); got != edit {
		t.Fatalf("edit screen = %d, want %d", got, edit)
	}
	if got := effectiveScreenIDOrFallback(resolved, true); got != view {
		t.Fatalf("view screen = %d, want %d", got, view)
	}

	// Unconfigured modes fall back individually; view may be absent while edit
	// resolves.
	partial := &repository.EffectiveConfig{EditScreenID: &edit}
	if got := effectiveScreenIDOrFallback(partial, true); got != repository.FallbackScreenID {
		t.Fatalf("unconfigured view screen = %d, want %d", got, repository.FallbackScreenID)
	}
}
