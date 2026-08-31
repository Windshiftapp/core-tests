package repoprep_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"windshift/internal/repoprep"
)

// TestPrepare_DestDirPlacesCheckout pins WI-449: when RepoSpec.DestDir is set,
// the per-run checkout is materialized there (a sibling dir under a shared
// multi-repo workspace root) instead of the default per-(workspace,slug)/runs
// location, while still being a full independent clone.
func TestPrepare_DestDirPlacesCheckout(t *testing.T) {
	origin, tip := seedOrigin(t)
	p := newPreparer(t)

	workspaceRoot := filepath.Join(t.TempDir(), "ws-root")
	dest := filepath.Join(workspaceRoot, "core-tests")

	pr, err := p.Prepare(context.Background(), repoprep.RepoSpec{
		WorkspaceID: 7,
		RepoSlug:    "acme/core-tests",
		RemoteURL:   origin,
		BaseRef:     "main",
		DestDir:     dest,
	}, 5)
	if err != nil {
		t.Fatalf("Prepare: %v", err)
	}
	if pr.Path != dest {
		t.Fatalf("checkout path: want %q, got %q", dest, pr.Path)
	}
	// Full, populated, independent clone at the requested location.
	if b, err := os.ReadFile(filepath.Join(dest, "README.md")); err != nil || string(b) != "hello\n" {
		t.Fatalf("README not materialized at DestDir: %q err=%v", string(b), err)
	}
	if pr.BaseCommit != tip {
		t.Errorf("base commit=%q want %q", pr.BaseCommit, tip)
	}
	// Two repos under one workspace root would be siblings; confirm the parent
	// is the workspace root we chose.
	if got := filepath.Dir(dest); got != workspaceRoot {
		t.Errorf("parent dir=%q want %q", got, workspaceRoot)
	}
}
