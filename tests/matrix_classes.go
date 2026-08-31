package tests

import "net/http"

// PermissionClass is a named (actor → expected status) row that the matrix
// asserts for every route classified into it. Each class encodes both the
// allow side (which actors get 2xx) and the deny side with the *exact* code
// the security policy mandates. AssertRejected-style permissive checks
// (accept any 401/403/404) are intentionally not used here — drift from
// 404→403 on workspace-permission denial is exactly the leak this matrix
// catches.
//
// Status-code policy (from MEMORY.md):
//
//	workspace-permission denial    → 404 (existence privacy)
//	cross-workspace direct-ID      → 404
//	per-record whitelist denial    → 403
//	global permission denial       → 403
//	token scope insufficient       → 403
//	no session / no token          → 401
type PermissionClass struct {
	Name        string              // e.g. "workspace.item.view"
	Description string              // human-readable note (which permission, why)
	Expected    map[MatrixActor]int // exact status per actor
}

// Classes is the canonical class registry. The matrix test iterates this
// list and runs every actor in MatrixActors against the representative
// route for each class (see matrix_routes.go).
//
// Seeded with one class — workspace.item.view — so the headline 404-not-403
// invariant gets coverage day one. Expand one class per resource family per
// commit (see plan: items.edit/delete, comments, attachments, channels,
// workflow, custom fields, time, approvals, tests, assets, teams, then
// workspace.admin + global.system.admin).
var Classes = []PermissionClass{
	{
		Name:        "workspace.item.view",
		Description: "GET /items/{id} — requires models.PermissionItemView on the item's workspace.",
		Expected: map[MatrixActor]int{
			ActorAnonymous:            http.StatusUnauthorized, // 401
			ActorNoMembership:         http.StatusNotFound,     // 404 — workspace-perm denial
			ActorCrossWorkspaceMember: http.StatusNotFound,     // 404 — cross-workspace direct-ID
			ActorWorkspaceViewer:      http.StatusOK,           // 200
			ActorWorkspaceEditor:      http.StatusOK,           // 200
			ActorWorkspaceAdmin:       http.StatusOK,           // 200
			ActorWorkspaceTester:      http.StatusOK,           // 200 — Tester role can VIEW items (only CRUD is gated)
			ActorSystemAdmin:          http.StatusOK,           // 200
			// ActorPortalCustomer (401): deferred — see matrix_helpers.go.
		},
	},
	{
		Name:        "workspace.item.edit",
		Description: "PUT /items/{id} — requires models.PermissionItemEdit. Item handler returns 404 on permission denial (existence privacy).",
		Expected: map[MatrixActor]int{
			ActorAnonymous:            http.StatusUnauthorized, // 401
			ActorNoMembership:         http.StatusNotFound,     // 404
			ActorCrossWorkspaceMember: http.StatusNotFound,     // 404
			ActorWorkspaceViewer:      http.StatusNotFound,     // 404 — Viewer lacks item.edit; canEditItem false → respondNotFound
			ActorWorkspaceEditor:      http.StatusOK,           // 200 — Editor has item.edit
			ActorWorkspaceAdmin:       http.StatusOK,           // 200
			// Tester deliberately lacks item.edit — that's the Editor/Tester role
			// distinction. Editor owns broad item editing; Tester owns
			// test.execute / test.manage. Both share view/create/comment.
			// Granting item.edit to Tester would collapse the roles.
			ActorWorkspaceTester: http.StatusNotFound, // 404
			ActorSystemAdmin:     http.StatusOK,       // 200
		},
	},
	{
		Name:        "workspace.item.comment",
		Description: "POST /items/{id}/comments — requires models.PermissionItemComment. Comment handler returns 404 on denial (existence privacy).",
		Expected: map[MatrixActor]int{
			ActorAnonymous:            http.StatusUnauthorized, // 401 — auth runs before body parse
			ActorNoMembership:         http.StatusNotFound,     // 404
			ActorCrossWorkspaceMember: http.StatusNotFound,     // 404
			ActorWorkspaceViewer:      http.StatusCreated,      // 201 — Viewer has item.comment
			ActorWorkspaceEditor:      http.StatusCreated,      // 201 — Editor has item.comment
			ActorWorkspaceAdmin:       http.StatusCreated,      // 201
			// Tester gets item.comment too (granted in permissions.sql alongside
			// the test.* perms): testers need to follow up on bugs they file.
			// See the Editor/Tester differentiation note on workspace.item.edit
			// below — Tester is deliberately a peer of Editor (overlap on
			// view/create/comment), not a strict subset.
			ActorWorkspaceTester: http.StatusCreated, // 201
			ActorSystemAdmin:     http.StatusCreated, // 201
		},
	},
	{
		Name: "workspace.admin",
		// Workspace handler diverges from the item-handler 404 policy: denial
		// is 403 (respondForbidden), not 404. Encode current behavior so the
		// matrix catches drift in either direction; the broader 403→404
		// alignment is a separate audit (see plan: out of scope).
		Description: "PUT /workspaces/{id} — requires models.PermissionWorkspaceAdmin. Workspace handler returns 403 on denial (unlike item handler).",
		Expected: map[MatrixActor]int{
			ActorAnonymous:            http.StatusUnauthorized, // 401
			ActorNoMembership:         http.StatusForbidden,    // 403 — workspace handler uses respondForbidden
			ActorCrossWorkspaceMember: http.StatusForbidden,    // 403
			ActorWorkspaceViewer:      http.StatusForbidden,    // 403
			ActorWorkspaceEditor:      http.StatusForbidden,    // 403 — Editor lacks workspace.admin
			ActorWorkspaceAdmin:       http.StatusOK,           // 200 — Administrator role has workspace.admin
			ActorWorkspaceTester:      http.StatusForbidden,    // 403
			ActorSystemAdmin:          http.StatusOK,           // 200
		},
	},
}

// ClassByName looks up a class. Returns nil if not found.
func ClassByName(name string) *PermissionClass {
	for i := range Classes {
		if Classes[i].Name == name {
			return &Classes[i]
		}
	}
	return nil
}
