//go:build test

package jiraimport

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestJiraImportPersistenceDoesNotLeakBackIntoHandlers(t *testing.T) {
	t.Parallel()
	handlerFiles, err := filepath.Glob(filepath.Join("..", "handlers", "jira_*.go"))
	if err != nil {
		t.Fatalf("glob Jira handler files: %v", err)
	}
	if len(handlerFiles) == 0 {
		t.Fatal("no Jira handler files found")
	}
	directDBCall := regexp.MustCompile(`(?:h\.db|tx)\.(Query|QueryRow|Exec|ExecWrite|Begin)\s*\(`)
	for _, path := range handlerFiles {
		name := filepath.Base(path)
		if filepath.Ext(name) != ".go" || len(name) >= len("_test.go") && name[len(name)-len("_test.go"):] == "_test.go" {
			continue
		}
		source, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if match := directDBCall.Find(source); match != nil {
			t.Errorf("%s calls %s directly; persistence belongs in internal/jiraimport or the owning repository/service", name, match)
		}
	}
}
