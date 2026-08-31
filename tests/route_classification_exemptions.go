package tests

// EnforcedPrefixes lists path prefixes that TestRouteClassification holds
// to the classification rule. A route whose Path starts with any of these
// prefixes must be present in MatrixRoutes or in RouteClassificationExemptions.
//
// The list grows incrementally — each new prefix is an explicit policy
// decision to bring that resource family under matrix coverage. Slice 4
// starts with /api/items only.
var EnforcedPrefixes = []string{
	"/api/items",
	"/api/comments",
	"/api/attachments",
	"/api/links",
}

// RouteClassificationExemption excludes a single registered route from the
// classification requirement. Reason is mandatory and surfaces in code
// review — every entry here must justify its place. Prefer adding a real
// MatrixRoute entry over a new exemption.
//
// Placeholder names are normalized at compare time, so `{id}` here matches
// a registered `{itemId}` (and vice versa). Use whichever name reads most
// naturally for the resource.
type RouteClassificationExemption struct {
	Method string
	Path   string // including the /api or /rest/api/v1 prefix
	Reason string
}

// RouteClassificationExemptions enumerates routes intentionally not in
// MatrixRoutes. Each entry must justify why with a durable reason — not a
// TODO marker. New routes should default to a MatrixRoute classification;
// add to this list only when the route genuinely doesn't fit any policy
// class.
var RouteClassificationExemptions = []RouteClassificationExemption{
	// Multi-workspace filtered list endpoints. These do not return 404 on
	// permission denial — they return 200 with a result list filtered by
	// the caller's workspace memberships. The matrix's exact-status model
	// doesn't capture this semantics cleanly; covered by Go integration
	// tests (TestItemListFiltering in permission_isolation_test.go) and
	// E2E specs (permissions-cross-workspace.spec.ts) instead.
	{Method: "GET", Path: "/api/items", Reason: "multi-workspace filtered list — 200 with filtered results, not 404"},
	{Method: "GET", Path: "/api/items/search", Reason: "multi-workspace filtered search — same semantics as GET /api/items"},
	{Method: "GET", Path: "/api/items/backlog", Reason: "multi-workspace filtered backlog — same semantics as GET /api/items"},
	{Method: "GET", Path: "/api/items/changes", Reason: "multi-workspace delta polling — returns filtered changed IDs/watermark, not per-item 404"},
	{Method: "GET", Path: "/api/items/batch", Reason: "multi-workspace filtered bulk fetch — 200 with inaccessible ids omitted, not per-item 404"},

	// Authenticated-only endpoint (no workspace permission gate). Returns
	// global cache statistics; behavior is auth-or-401 with no per-actor
	// distinction beyond that. Not a fit for any workspace-scoped class.
	{Method: "GET", Path: "/api/items/cache-stats", Reason: "auth-only, no workspace permission gate"},

	// New-resource creation. Idempotency of repeated calls across actors
	// requires per-actor fixture refresh (each successful create pollutes
	// state); design decision still pending. Coverage exists via existing
	// integration tests for the canEdit path.
	{Method: "POST", Path: "/api/items", Reason: "create endpoint — needs workspace.item.create class + per-actor fixture refresh"},
	{Method: "POST", Path: "/api/items/{id}/move-workspace/preview", Reason: "cross-workspace operation requires source item.edit plus destination item.create; covered by focused dual-workspace authorization tests"},
	{Method: "POST", Path: "/api/items/{id}/move-workspace", Reason: "cross-workspace mutation requires source item.edit plus destination item.create and consumes the source fixture; covered by focused dual-workspace authorization tests"},

	// Destructive ops. Adding a representative for workspace.item.delete
	// would have the first successful actor consume the fixture, breaking
	// subsequent actor iterations. Stays exempted until per-actor fixture
	// refresh lands; meanwhile classification intent is documented here.
	{Method: "DELETE", Path: "/api/items/{id}", Reason: "destructive (workspace.item.delete intent) — needs per-actor fixture refresh"},
	{Method: "DELETE", Path: "/api/items/{id}/cascade", Reason: "destructive (workspace.item.delete intent) — needs per-actor fixture refresh"},
	{Method: "DELETE", Path: "/api/items/{id}/watch", Reason: "destructive (workspace.item.view intent — toggles state) — needs per-actor fixture refresh"},
	{Method: "DELETE", Path: "/api/items/{id}/unschedule", Reason: "destructive (workspace.item.edit intent — consumes schedule fixture)"},
	{Method: "DELETE", Path: "/api/items/{id}/recurrence", Reason: "destructive (workspace.item.edit intent — consumes recurrence fixture)"},
	{Method: "DELETE", Path: "/api/items/{id}/labels/{labelId}", Reason: "destructive (workspace.item.edit intent — needs label fixture)"},
	{Method: "DELETE", Path: "/api/items/{id}/personal-labels/{labelId}", Reason: "destructive (workspace.item.edit intent — needs personal-label fixture)"},

	// Endpoints with non-workspace-scoped gates. These don't fit the
	// 8-actor workspace matrix model; coverage belongs in their own
	// focused tests.
	{Method: "DELETE", Path: "/api/items/{id}/related-work-item", Reason: "owner/personal-workspace gate (not workspace-role gated) — different policy model"},
	{Method: "GET", Path: "/api/items/{id}/attachments", Reason: "gates on CanModifyItemAttachment via service — covered by attachments-idor.spec.ts"},
	{Method: "GET", Path: "/api/items/{id}/events", Reason: "SSE stream (PermissionItemView-gated) — the matrix driver would hang on the long-lived response; covered by item-detail-sse-live.spec.ts"},
	{Method: "POST", Path: "/api/items/{itemId}/agent-runs", Reason: "agent-run rerun (PermissionItemEdit-gated) — spawns a coding-agent run; per-actor fixture refresh + runner stubbing needed before matrixing"},

	// /api/comments — comment edit/delete have owner-or-comment.edit_others
	// gate (not a simple workspace permission). Needs a dedicated
	// `workspace.comment.edit` class with 2 separate fixtures (own-comment
	// + other-user comment). Deferred to a focused slice.
	{Method: "PUT", Path: "/api/comments/{id}", Reason: "owner-or-comment.edit_others gate — needs dedicated workspace.comment.edit class with 2-fixture setup"},
	{Method: "DELETE", Path: "/api/comments/{id}", Reason: "owner-or-comment.edit_others gate — destructive + needs 2-fixture setup"},

	// /api/attachments — DELETE consumes the fixture; needs per-actor
	// refresh before being exercised. Classification intent is workspace.item.edit.
	{Method: "DELETE", Path: "/api/attachments/{attachmentId}", Reason: "destructive (workspace.item.edit intent) — needs per-actor fixture refresh"},

	// /api/links — DELETE consumes the link fixture; same as attachments.
	{Method: "DELETE", Path: "/api/links/{linkId}", Reason: "destructive (workspace.item.edit intent) — needs per-actor fixture refresh"},

	// Multi-workspace filtered list endpoint — same semantics as the
	// other filtered-list exemptions above.
	{Method: "GET", Path: "/api/links/search", Reason: "multi-workspace filtered search for linkable items — 200 with filtered results, not 404"},
	{Method: "GET", Path: "/api/links/batch", Reason: "multi-workspace filtered batch links fetch — 200 with per-item filtered results (empty for inaccessible items), not 404"},
}
