package tests

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// RegisteredRoute is a (method, path) pair harvested from the source by
// walking the routes/ package's AST. Path always includes the surface
// prefix ("/api" or "/rest/api/v1") so the matrix tests and
// drift-guards can compare directly against incoming request paths.
type RegisteredRoute struct {
	Method string
	Path   string
}

// EnumerateRegisteredRoutes returns every HTTP route registered by the
// running server, parsed from the source tree. It walks two surfaces:
//
//  1. internal/restapi/v1/router.go     — calls to HandleWithMiddleware
//     (string-literal first arg)        — prefix /rest/api/v1
//  2. internal/routes/*.go              — calls to .HandleH / .Handle on
//     the `api` route group              — prefix /api
//
// Direct mux.Handle("METHOD /path", ...) registrations in server.go (the
// logbook/LLM proxies) are intentionally excluded — they bypass the
// middleware chain and are mostly internal. Add them if a test surfaces a
// concrete need.
//
// The function fails the test if it cannot locate the routes/ directory
// from any of the candidate roots — silent zero-result is too easy to
// miss.
func EnumerateRegisteredRoutes(t *testing.T) []RegisteredRoute {
	t.Helper()

	coreRoot := locateCoreRoot(t)

	var all []RegisteredRoute
	all = append(all, parseV1Router(t, filepath.Join(coreRoot, "internal", "restapi", "v1", "router.go"))...)
	all = append(all, parseRoutesPackage(t, filepath.Join(coreRoot, "internal", "routes"))...)

	// Stable order — both for deterministic subtest names and for diff
	// readability when the route table grows.
	sort.SliceStable(all, func(i, j int) bool {
		if all[i].Path != all[j].Path {
			return all[i].Path < all[j].Path
		}
		return all[i].Method < all[j].Method
	})
	return all
}

// locateCoreRoot finds the on-disk root of the core repo. Two layouts are
// supported because tests can run either directly from core-tests/tests/
// (during local dev with go test) or from a temp overlay (overlay.sh).
func locateCoreRoot(t *testing.T) string {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	candidates := []string{
		filepath.Join(filepath.Dir(thisFile), ".."),               // overlay copy: core/tests/ → core/
		filepath.Join(filepath.Dir(thisFile), "..", "..", "core"), // dev layout: core-tests/tests/ → ../core
	}
	for _, c := range candidates {
		if _, err := os.Stat(filepath.Join(c, "internal", "routes", "routes.go")); err == nil {
			return c
		}
	}
	t.Fatalf("could not locate core repo root from any of: %v", candidates)
	return ""
}

const (
	apiPrefix = "/api"
	v1Prefix  = "/rest/api/v1"
)

// parseV1Router harvests routes from internal/restapi/v1/router.go. It
// looks for HandleWithMiddleware("METHOD /path", ...) calls and prepends
// /rest/api/v1 (the route group's prefix).
func parseV1Router(t *testing.T, path string) []RegisteredRoute {
	t.Helper()

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}

	var routes []RegisteredRoute
	ast.Inspect(f, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := ce.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel == nil || sel.Sel.Name != "HandleWithMiddleware" {
			return true
		}
		method, path, ok := extractMethodPath(ce)
		if !ok {
			return true
		}
		routes = append(routes, RegisteredRoute{Method: method, Path: v1Prefix + path})
		return true
	})
	return routes
}

// parseRoutesPackage walks every *.go file under internal/routes/ and
// extracts calls of the form <recv>.Handle(...) and <recv>.HandleH(...)
// where the first argument is a string literal "METHOD /path".
//
// The receiver's URL prefix is resolved per-function by scanning the
// function body for short assignments of the form `<name> := deps.API`
// then consulting depsFieldToPrefix.
// This is brittler than tracking types via go/types, but is intentionally
// chosen over the typed approach: a typed analysis would need to load
// the full module and we want the enumerator to remain a fast,
// dependency-free AST walk.
func parseRoutesPackage(t *testing.T, dir string) []RegisteredRoute {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}

	var routes []RegisteredRoute
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		if strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}

		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			localPrefix := localGroupPrefixes(fd.Body)
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				ce, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := ce.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel == nil {
					return true
				}
				if sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleH" {
					return true
				}
				recv, ok := sel.X.(*ast.Ident)
				if !ok {
					return true
				}
				prefix, ok := localPrefix[recv.Name]
				if !ok {
					return true
				}
				method, p, ok := extractMethodPath(ce)
				if !ok {
					return true
				}
				routes = append(routes, RegisteredRoute{Method: method, Path: prefix + p})
				return true
			})
		}
	}
	return routes
}

// depsFieldToPrefix maps the route-group fields on routes.Deps to the URL
// prefix the group mounts on. Kept in sync with internal/server/server.go
// where the groups are constructed.
var depsFieldToPrefix = map[string]string{
	"API": apiPrefix,
}

// localGroupPrefixes scans a function body for short variable declarations
// of the form `<name> := deps.<Field>` where <Field> is one of the keys in
// depsFieldToPrefix, and returns the resulting local-name → prefix map.
// Used by parseRoutesPackage so a Handle call dispatched on a locally-named
// route group is correctly prefixed.
func localGroupPrefixes(body *ast.BlockStmt) map[string]string {
	out := map[string]string{}
	ast.Inspect(body, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || as.Tok != token.DEFINE {
			return true
		}
		if len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		lhs, ok := as.Lhs[0].(*ast.Ident)
		if !ok {
			return true
		}
		sel, ok := as.Rhs[0].(*ast.SelectorExpr)
		if !ok {
			return true
		}
		recv, ok := sel.X.(*ast.Ident)
		if !ok || recv.Name != "deps" || sel.Sel == nil {
			return true
		}
		if prefix, ok := depsFieldToPrefix[sel.Sel.Name]; ok {
			out[lhs.Name] = prefix
		}
		return true
	})
	return out
}

// extractMethodPath pulls the "METHOD /path" string-literal first arg from
// a Handle/HandleH/HandleWithMiddleware call expression. Returns ok=false
// if the first arg is not a string literal or doesn't split into two
// space-separated tokens (i.e. patterns like just "/foo" without a method).
func extractMethodPath(ce *ast.CallExpr) (method, path string, ok bool) {
	if len(ce.Args) == 0 {
		return "", "", false
	}
	bl, ok := ce.Args[0].(*ast.BasicLit)
	if !ok || bl.Kind != token.STRING {
		return "", "", false
	}
	raw := strings.Trim(bl.Value, `"`)
	parts := strings.SplitN(raw, " ", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}
