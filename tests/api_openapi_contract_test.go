package tests

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
)

// TestAPIOpenAPIContract is the v1 REST API contract test. It loads the
// committed OpenAPI 3.0 spec, walks the route table declared in router.go,
// and asserts:
//
//  1. Every route registered with HandleWithMiddleware in router.go has a
//     matching path+method in the spec. Catches "handler exists but is
//     undocumented" drift.
//  2. A representative set of GET endpoints, when driven against a live
//     test server with a valid bearer token, return responses that
//     validate against the spec's declared 200 schema. Catches "response
//     shape changed but spec didn't".
//
// Pre-commit hook + CI re-run `make openapi` to keep the spec in sync with
// handler annotations; this test is the second line of defense against
// the spec lying about what handlers actually return.
func TestAPIOpenAPIContract(t *testing.T) {
	doc := loadOpenAPISpec(t)
	router := buildLegacyRouter(t, doc)

	t.Run("RouteCoverage", func(t *testing.T) {
		assertEveryRegisteredRouteInSpec(t, doc)
	})

	t.Run("ResponseValidation", func(t *testing.T) {
		ts, cleanup := StartTestServer(t, GetDBType())
		defer cleanup()
		CreateBearerToken(t, ts)

		smokeEndpoints := []struct {
			name   string
			method string
			path   string
			body   interface{}
		}{
			{"list_workspaces", http.MethodGet, "/workspaces", nil},
			{"list_items", http.MethodGet, "/items", nil},
			{"list_statuses", http.MethodGet, "/statuses", nil},
			{"list_status_categories", http.MethodGet, "/status-categories", nil},
			{"list_workflows", http.MethodGet, "/workflows", nil},
			{"list_priorities", http.MethodGet, "/priorities", nil},
			{"list_item_types", http.MethodGet, "/item-types", nil},
			{"list_custom_fields", http.MethodGet, "/custom-fields", nil},
			{"get_current_user", http.MethodGet, "/users/me", nil},
			{"list_milestones", http.MethodGet, "/milestones", nil},
			{"list_iterations", http.MethodGet, "/iterations", nil},
		}

		for _, ep := range smokeEndpoints {
			t.Run(ep.name, func(t *testing.T) {
				fullPath := "/rest/api/v1" + ep.path
				resp := MakeBearerRequestWithToken(t, ts, ts.BearerToken, ep.method, fullPath, ep.body)
				defer resp.Body.Close()

				body, err := io.ReadAll(resp.Body)
				if err != nil {
					t.Fatalf("read body: %v", err)
				}

				if resp.StatusCode != http.StatusOK {
					t.Fatalf("%s %s: expected 200, got %d body=%s",
						ep.method, fullPath, resp.StatusCode, string(body))
				}

				validateResponseAgainstSpec(t, router, ep.method, fullPath, resp.StatusCode, body, resp.Header.Get("Content-Type"))
			})
		}
	})
}

func TestOperationalOpenAPIContract(t *testing.T) {
	doc := loadOpenAPISpec(t)

	tests := []struct {
		path         string
		statuses     []string
		contentTypes []string
	}{
		{
			path:         "/healthz",
			statuses:     []string{"200"},
			contentTypes: []string{"application/json"},
		},
		{
			path:         "/readyz",
			statuses:     []string{"200", "503"},
			contentTypes: []string{"application/json"},
		},
		{
			path:         "/metrics",
			statuses:     []string{"200"},
			contentTypes: []string{"text/plain", "application/openmetrics-text"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			pathItem := doc.Paths.Find(tt.path)
			if pathItem == nil {
				t.Fatalf("OpenAPI path %q is missing", tt.path)
			}
			if len(pathItem.Servers) != 1 || pathItem.Servers[0].URL != "/" {
				t.Fatalf("OpenAPI path %q servers = %#v, want root server /", tt.path, pathItem.Servers)
			}

			operation := pathItem.Get
			if operation == nil {
				t.Fatalf("OpenAPI path %q has no GET operation", tt.path)
			}
			if len(operation.Tags) != 1 || operation.Tags[0] != "operations" {
				t.Fatalf("OpenAPI path %q tags = %v, want [operations]", tt.path, operation.Tags)
			}
			if operation.Security != nil && len(*operation.Security) != 0 {
				t.Fatalf("OpenAPI path %q security = %#v, want public operation", tt.path, *operation.Security)
			}

			for _, status := range tt.statuses {
				response := operation.Responses.Value(status)
				if response == nil || response.Value == nil {
					t.Fatalf("OpenAPI path %q response %s is missing", tt.path, status)
				}
				for _, contentType := range tt.contentTypes {
					if response.Value.Content.Get(contentType) == nil {
						t.Errorf("OpenAPI path %q response %s content type %q is missing", tt.path, status, contentType)
					}
				}
			}
		})
	}
}

// loadOpenAPISpec reads core/api/openapi.yaml relative to the on-disk location
// of this test file. Uses runtime.Caller so it works whether the test is run
// from core-tests/tests/ directly or from core/tests/ via overlay.sh.
func loadOpenAPISpec(t *testing.T) *openapi3.T {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	specPath := filepath.Join(filepath.Dir(thisFile), "..", "api", "openapi.yaml")
	// The overlay copy places this file at core/tests/, with the spec at
	// core/api/openapi.yaml. The original lives at core-tests/tests/, with
	// the spec at ../core/api/openapi.yaml. Try the obvious sibling first,
	// then fall back to the overlay layout.
	if _, err := openapi3.NewLoader().LoadFromFile(specPath); err != nil {
		alt := filepath.Join(filepath.Dir(thisFile), "..", "..", "core", "api", "openapi.yaml")
		if _, err2 := openapi3.NewLoader().LoadFromFile(alt); err2 == nil {
			specPath = alt
		}
	}

	loader := openapi3.NewLoader()
	loader.IsExternalRefsAllowed = false
	doc, err := loader.LoadFromFile(specPath)
	if err != nil {
		t.Fatalf("load spec from %s: %v", specPath, err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatalf("validate spec: %v", err)
	}
	return doc
}

func buildLegacyRouter(t *testing.T, doc *openapi3.T) routers.Router {
	t.Helper()
	r, err := legacy.NewRouter(doc)
	if err != nil {
		t.Fatalf("build router: %v", err)
	}
	return r
}

// validateResponseAgainstSpec runs kin-openapi's response validator against a
// captured response. The test server's actual base URL varies per run, so we
// build a synthetic request URL prefixed with the spec's first server URL
// to make routing deterministic.
func validateResponseAgainstSpec(t *testing.T, router routers.Router, method, fullPath string, status int, body []byte, contentType string) {
	t.Helper()

	// Pass a path-only URL: kin-openapi/legacy router compares the spec's
	// relative server URL ("/rest/api/v1") against the raw URL string, and
	// http://host/... won't match a "/..." pattern.
	req, err := http.NewRequest(method, fullPath, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	route, pathParams, err := router.FindRoute(req)
	if err != nil {
		t.Fatalf("FindRoute %s %s: %v", method, fullPath, err)
	}

	respInput := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{
			Request:    req,
			PathParams: pathParams,
			Route:      route,
			Options: &openapi3filter.Options{
				AuthenticationFunc: openapi3filter.NoopAuthenticationFunc,
			},
		},
		Status: status,
		Header: http.Header{"Content-Type": []string{contentType}},
		Body:   io.NopCloser(bytes.NewReader(body)),
	}

	if err := openapi3filter.ValidateResponse(context.Background(), respInput); err != nil {
		t.Fatalf("response validation %s %s -> %d: %v\nbody=%s",
			method, fullPath, status, err, string(body))
	}
}

// assertEveryRegisteredRouteInSpec parses internal/restapi/v1/router.go,
// extracts every route registered via HandleWithMiddleware, and asserts the
// spec documents each (path, method) pair. Catches handlers that landed
// without an annotation block.
func assertEveryRegisteredRouteInSpec(t *testing.T, doc *openapi3.T) {
	t.Helper()

	registered := collectRegisteredRoutes(t)
	if len(registered) == 0 {
		t.Fatal("collectRegisteredRoutes returned 0 — parser likely failed silently")
	}

	const v1Prefix = "/rest/api/v1"

	var missing []string
	for _, r := range registered {
		// Strip the v1 prefix added by RouteGroup; spec paths are stored
		// without it (basePath holds the prefix).
		specPath := strings.TrimPrefix(r.Path, v1Prefix)
		if specPath == r.Path {
			specPath = r.Path // no prefix found, use as-is
		}

		pi := doc.Paths.Find(specPath)
		if pi == nil {
			missing = append(missing, fmt.Sprintf("%s %s (path missing)", r.Method, specPath))
			continue
		}
		op := pi.GetOperation(r.Method)
		if op == nil {
			missing = append(missing, fmt.Sprintf("%s %s (path present, method missing)", r.Method, specPath))
		}
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("router.go declares routes that are not documented in api/openapi.yaml — add swag annotations and run `make openapi`:\n  %s",
			strings.Join(missing, "\n  "))
	}
}

type registeredRoute struct {
	Method string
	Path   string
}

// collectRegisteredRoutes parses router.go and harvests every literal route
// passed to HandleWithMiddleware as the first argument (e.g. "GET /items").
// Skips RouteGroup prefixes — those are applied by RouteGroup at runtime;
// this function reports the per-route path as written in the source. The
// caller is responsible for stripping the /rest/api/v1 RouteGroup prefix
// before comparing to the spec.
func collectRegisteredRoutes(t *testing.T) []registeredRoute {
	t.Helper()

	_, thisFile, _, _ := runtime.Caller(0)
	candidates := []string{
		filepath.Join(filepath.Dir(thisFile), "..", "internal", "restapi", "v1", "router.go"),
		filepath.Join(filepath.Dir(thisFile), "..", "..", "core", "internal", "restapi", "v1", "router.go"),
	}
	var routerPath string
	for _, c := range candidates {
		if _, err := parser.ParseFile(token.NewFileSet(), c, nil, parser.PackageClauseOnly); err == nil {
			routerPath = c
			break
		}
	}
	if routerPath == "" {
		t.Fatalf("could not locate router.go from any of: %v", candidates)
	}

	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, routerPath, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", routerPath, err)
	}

	const v1Prefix = "/rest/api/v1"

	var routes []registeredRoute
	ast.Inspect(f, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := ce.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel == nil || sel.Sel.Name != "HandleWithMiddleware" {
			return true
		}
		if len(ce.Args) == 0 {
			return true
		}
		bl, ok := ce.Args[0].(*ast.BasicLit)
		if !ok || bl.Kind != token.STRING {
			return true
		}
		raw := strings.Trim(bl.Value, `"`)
		parts := strings.SplitN(raw, " ", 2)
		if len(parts) != 2 {
			return true
		}
		method, path := parts[0], parts[1]
		// All v1 routes register on a RouteGroup with the /rest/api/v1
		// prefix; conceptually their full path includes that prefix.
		// Prepend it for comparison consistency.
		routes = append(routes, registeredRoute{Method: method, Path: v1Prefix + path})
		return true
	})

	return routes
}
