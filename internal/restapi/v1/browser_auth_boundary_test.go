//go:build test

package v1_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestBrowserDoesNotUseBearerAPI(t *testing.T) {
	root := findModuleRoot(t)
	frontendRoot := filepath.Join(root, "frontend", "src")
	credentialUse := regexp.MustCompile(`(?is)((fetch|headers|authorization).{0,240}crw_|crw_.{0,240}(fetch|headers|authorization))`)
	publicOpenAPISpec := regexp.MustCompile(`/rest/api/v[0-9]+/openapi\.json`)

	var violations []string
	err := filepath.WalkDir(frontendRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".js" && ext != ".ts" && ext != ".svelte" {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}

		source := string(data)
		operationalSource := publicOpenAPISpec.ReplaceAllString(source, "")
		if strings.Contains(operationalSource, "/rest/api/v") || credentialUse.MatchString(source) {
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			violations = append(violations, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan frontend sources: %v", err)
	}
	if len(violations) > 0 {
		t.Fatalf("P1 security boundary violation: browser code must use session-cookie /api routes and must never send crw_* bearer credentials; offending files:\n  %s", strings.Join(violations, "\n  "))
	}
}

func findModuleRoot(t *testing.T) string {
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
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}
