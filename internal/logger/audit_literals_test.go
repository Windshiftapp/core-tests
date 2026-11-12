//go:build test

package logger

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// auditTaxonomyFile owns the action/resource constants and is the only place
// where the raw strings may appear.
const auditTaxonomyFile = "internal/logger/audit.go"

// TestNoRawAuditLiterals is the WI-1899 regression guard. Before the taxonomy
// cleanup, ~36 call sites passed action/resource strings inline (for example
// "asset_report_create" / "asset_report" and the agent-binding actions), so a
// rename could not be enforced and the taxonomy drifted between underscore and
// resource.verb naming. This test fails if any audit call site or AuditEvent
// literal passes a raw string for its action or resource, forcing authors to
// add or reuse a logger constant instead.
func TestNoRawAuditLiterals(t *testing.T) {
	root := auditModuleRoot(t)
	var violations []string

	walkErr := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		if rel == auditTaxonomyFile {
			return nil
		}

		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		ast.Inspect(file, func(node ast.Node) bool {
			switch typed := node.(type) {
			case *ast.CallExpr:
				if action, resource, ok := auditCallArgs(typed); ok {
					violations = appendRawAuditLiterals(violations, rel, action, resource)
				}
			case *ast.CompositeLit:
				if action, resource, ok := auditEventFields(typed); ok {
					violations = appendRawAuditLiterals(violations, rel, action, resource)
				}
			}
			return true
		})
		return nil
	})
	if walkErr != nil {
		t.Fatalf("scan audit call sites: %v", walkErr)
	}

	if len(violations) > 0 {
		t.Errorf("raw audit action/resource literals found — use a constant from internal/logger/audit.go:\n  %s",
			strings.Join(violations, "\n  "))
	}
}

func appendRawAuditLiterals(violations []string, rel string, action, resource ast.Expr) []string {
	if lit, ok := action.(*ast.BasicLit); ok && lit.Kind == token.STRING {
		violations = append(violations, rel+": raw action literal "+lit.Value)
	}
	if lit, ok := resource.(*ast.BasicLit); ok && lit.Kind == token.STRING {
		violations = append(violations, rel+": raw resource literal "+lit.Value)
	}
	return violations
}

// auditCallArgs returns the action and resource expressions for the audit
// helpers, and whether the call is a recognised audit helper with both
// positions present.
func auditCallArgs(call *ast.CallExpr) (ast.Expr, ast.Expr, bool) {
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		switch fun.Sel.Name {
		case "Log", "LogWithDetails", "LogFailure", "LogDenied":
			if len(call.Args) >= 4 {
				return call.Args[2], call.Args[3], true
			}
		}
	case *ast.Ident:
		if fun.Name == "emitServiceAudit" && len(call.Args) >= 4 {
			return call.Args[2], call.Args[3], true
		}
	}
	return nil, nil, false
}

// auditEventFields returns the ActionType and ResourceType expressions of an
// AuditEvent composite literal.
func auditEventFields(lit *ast.CompositeLit) (ast.Expr, ast.Expr, bool) {
	if !isAuditEventType(lit.Type) {
		return nil, nil, false
	}
	var action, resource ast.Expr
	for _, element := range lit.Elts {
		pair, ok := element.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := pair.Key.(*ast.Ident)
		if !ok {
			continue
		}
		switch key.Name {
		case "ActionType":
			action = pair.Value
		case "ResourceType":
			resource = pair.Value
		}
	}
	return action, resource, action != nil || resource != nil
}

func isAuditEventType(expr ast.Expr) bool {
	switch typed := expr.(type) {
	case *ast.Ident:
		return typed.Name == "AuditEvent"
	case *ast.SelectorExpr:
		return typed.Sel.Name == "AuditEvent"
	}
	return false
}

// auditModuleRoot walks up from the test working directory to the module root.
func auditModuleRoot(t *testing.T) string {
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
