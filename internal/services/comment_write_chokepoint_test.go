//go:build test

package services

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestNoDirectCommentTableWrites is the WI-483 bypass guard. Comment-table
// writes must live only in comment_service.go — the single chokepoint that
// publishes the item-change event after commit. Any INSERT/UPDATE/DELETE
// against the `comments` table anywhere else under internal/ fails this test,
// so a future direct-SQL comment write (the exact bypass class this work
// removed) cannot slip back in silently.
//
// The patterns are SQL-specific (require INTO/FROM/SET) to avoid matching
// English prose, and the trailing word boundary excludes the unrelated
// `issue_sync_comments` tracking table.
func TestNoDirectCommentTableWrites(t *testing.T) {
	root := moduleRoot(t)
	pattern := regexp.MustCompile(`(?i)(insert\s+into\s+comments\b|delete\s+from\s+comments\b|update\s+comments\s+set)`)
	allowed := filepath.Join("internal", "services", "comment_service.go")

	var violations []string
	walkRoot := filepath.Join(root, "internal")
	err := filepath.WalkDir(walkRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if rel == allowed {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if pattern.Match(data) {
			violations = append(violations, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", walkRoot, err)
	}
	if len(violations) > 0 {
		t.Errorf("direct comments-table writes found outside comment_service.go — route them through CommentService so the item-change publish (WI-483) is not bypassed:\n  %s",
			strings.Join(violations, "\n  "))
	}
}

// moduleRoot walks up from the test's working directory to the module root
// (the directory containing go.mod).
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found walking up from working directory")
		}
		dir = parent
	}
}
