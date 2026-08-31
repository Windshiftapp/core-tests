package tests

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

// MatrixActor names a test subject in the permission matrix. Each actor
// represents a distinct caller profile (anonymous, role-X member, system
// admin, portal customer) that gets the same expected status for every cell
// of its assigned permission class.
type MatrixActor string

const (
	ActorAnonymous            MatrixActor = "anonymous"
	ActorNoMembership         MatrixActor = "no_membership"
	ActorCrossWorkspaceMember MatrixActor = "cross_workspace"
	ActorWorkspaceViewer      MatrixActor = "ws_viewer"
	ActorWorkspaceEditor      MatrixActor = "ws_editor"
	ActorWorkspaceAdmin       MatrixActor = "ws_admin"
	ActorWorkspaceTester      MatrixActor = "ws_tester"
	ActorSystemAdmin          MatrixActor = "system_admin"

	// ActorPortalCustomer is deferred: the WIP change to
	// internal/handlers/portal_customers.go (CreatePortalCustomer) now hard-
	// requires a seeded 'Portal Customer' contact_role that does not exist
	// in the schema. Every portal-customer integration test is currently
	// red on the working tree (TestPortalCustomer_CannotAccessInternalEndpoints
	// fails to create the customer with a 500). Once that seed is added back
	// (or the lookup is reverted to its previous best-effort behavior),
	// reintroduce ActorPortalCustomer here and add its expected statuses to
	// every PermissionClass.
	// ActorPortalCustomer MatrixActor = "portal_customer"
)

// MatrixActors is the canonical iteration order. Anything that walks the
// actor set should range over this slice, not the map, so subtest output is
// deterministic.
var MatrixActors = []MatrixActor{
	ActorAnonymous,
	ActorNoMembership,
	ActorCrossWorkspaceMember,
	ActorWorkspaceViewer,
	ActorWorkspaceEditor,
	ActorWorkspaceAdmin,
	ActorWorkspaceTester,
	ActorSystemAdmin,
	// ActorPortalCustomer deferred — see comment on the const block.
}

// MatrixSession is one actor's pre-built request transport. Do hits /api/*
// with whatever auth flavor the actor uses (session cookie, no-auth, portal
// bearer); the matrix test code never needs to know which.
type MatrixSession struct {
	Name   MatrixActor
	UserID int // 0 for ActorAnonymous and ActorPortalCustomer
	do     func(t *testing.T, method, path string, body interface{}) *http.Response
}

// Do dispatches the request through this actor's transport.
func (s *MatrixSession) Do(t *testing.T, method, path string, body interface{}) *http.Response {
	t.Helper()
	return s.do(t, method, path, body)
}

// MatrixSubjects bundles the workspace fixtures every actor is configured
// against. TargetWorkspaceID is what each matrix cell hits; OtherWorkspaceID
// exists so ActorCrossWorkspaceMember has a real role somewhere else and
// is still rejected on the target.
type MatrixSubjects struct {
	TargetWorkspaceID  int
	TargetWorkspaceKey string
	OtherWorkspaceID   int
}

// MatrixFixtures bundles every seeded ID an ExpandMatrixPath template might
// reference. The matrix test seeds these once (workspaces, item, comment,
// attachment, link, ...) and reuses them across every (class, actor) cell.
//
// Fields are zero-valued when the corresponding fixture hasn't been seeded
// yet; route templates that reference an unseeded ID will produce "/0" path
// segments, which is intentionally invalid so the green-up pass surfaces
// missing fixtures as 4xx mismatches rather than silent passes.
//
// Scope of the matrix — locked-down workspaces only:
//
// TestPermissionMatrix runs against a workspace that LockDownWorkspace
// (helpers.go) has explicitly assigned at least one role to. That assignment
// sets wsExplicit[viewerRoleID] = true in permission_cache.go:1024, which
// zeroes the WorkspaceEveryone bonus map (permission_cache.go:1026). In this
// configuration, each actor gets only the permissions of the role(s)
// explicitly granted to them — no "open workspace" everyone bonus.
//
// The Viewer→Editor→Tester everyone-bonus chain in permission_cache.go:962
// is therefore NOT exercised by this matrix. In a real workspace where
// roles are unassigned (the default state for new workspaces), Tester
// users would inherit Editor's perms via that chain — but the matrix
// asserts the strictest configuration only.
//
// Covering the open-workspace path is a separate initiative (see plan
// Slice 9). The status codes encoded in matrix_classes.go reflect the
// locked-down state.
type MatrixFixtures struct {
	TargetWorkspaceID  int
	OtherWorkspaceID   int
	TargetItemID       int
	OtherItemID        int
	TargetCommentID    int
	TargetAttachmentID int
	TargetLinkID       int
	PortalSlug         string
}

// SetupMatrixActors builds one MatrixSession per actor, assuming the caller
// has already:
//   - started the test server,
//   - called CreateBearerToken to seed the admin user + bearer token,
//   - created the target and "other" workspaces,
//   - locked the target workspace down so that workspace-permission denial
//     manifests as 404 (the security policy invariant the matrix exists to
//     guard).
//
// Reused helpers:
//   - CreateTestUserWithCredentials for human actors
//   - AssignWorkspaceRole for role-bound actors
//   - GrantGlobalPermission for ActorSystemAdmin
//   - CreateBearerTokenForUser (returns session cookie — misnamed but stable)
//   - CreatePortalCustomerWithSession for ActorPortalCustomer
//   - MakeAuthRequestWithToken / MakeUnauthenticatedRequest / MakePortalRequest for transport
func SetupMatrixActors(t *testing.T, server *TestServer, subj MatrixSubjects) map[MatrixActor]*MatrixSession {
	t.Helper()

	// Short suffix (last 8 nanos) keeps usernames under the 32-char limit
	// while remaining unique across parallel matrix runs.
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	if len(suffix) > 8 {
		suffix = suffix[len(suffix)-8:]
	}

	sessions := map[MatrixActor]*MatrixSession{}

	// Anonymous: no auth header at all.
	sessions[ActorAnonymous] = &MatrixSession{
		Name: ActorAnonymous,
		do: func(t *testing.T, method, path string, body interface{}) *http.Response {
			return MakeUnauthenticatedRequest(t, server, method, path, body)
		},
	}

	// NoMembership: authenticated but assigned no role on the target workspace.
	// Since the target is locked down, a missing role manifests as 404.
	noMemberID, noMemberUser, noMemberPass := CreateTestUserWithCredentials(
		t, server,
		"mx_nomemb_"+suffix,
		"mx_nomemb_"+suffix+"@test.com",
	)
	noMemberCookie := CreateBearerTokenForUser(t, server, noMemberUser, noMemberPass)
	sessions[ActorNoMembership] = &MatrixSession{
		Name:   ActorNoMembership,
		UserID: noMemberID,
		do: func(t *testing.T, method, path string, body interface{}) *http.Response {
			return MakeAuthRequestWithToken(t, server, noMemberCookie, method, path, body)
		},
	}

	// CrossWorkspaceMember: Editor on OtherWorkspaceID, no role on target.
	// Must still be rejected on the target workspace.
	crossID, crossUser, crossPass := CreateTestUserWithCredentials(
		t, server,
		"mx_cross_"+suffix,
		"mx_cross_"+suffix+"@test.com",
	)
	AssignWorkspaceRole(t, server, crossID, subj.OtherWorkspaceID, "Editor")
	crossCookie := CreateBearerTokenForUser(t, server, crossUser, crossPass)
	sessions[ActorCrossWorkspaceMember] = &MatrixSession{
		Name:   ActorCrossWorkspaceMember,
		UserID: crossID,
		do: func(t *testing.T, method, path string, body interface{}) *http.Response {
			return MakeAuthRequestWithToken(t, server, crossCookie, method, path, body)
		},
	}

	// Workspace role actors: Viewer, Editor, Administrator, Tester.
	for _, spec := range []struct {
		actor    MatrixActor
		short    string
		roleName string
	}{
		{ActorWorkspaceViewer, "view", "Viewer"},
		{ActorWorkspaceEditor, "edit", "Editor"},
		{ActorWorkspaceAdmin, "wadm", "Administrator"},
		{ActorWorkspaceTester, "test", "Tester"},
	} {
		id, uname, pass := CreateTestUserWithCredentials(
			t, server,
			fmt.Sprintf("mx_%s_%s", spec.short, suffix),
			fmt.Sprintf("mx_%s_%s@test.com", spec.short, suffix),
		)
		AssignWorkspaceRole(t, server, id, subj.TargetWorkspaceID, spec.roleName)
		cookie := CreateBearerTokenForUser(t, server, uname, pass)
		sessions[spec.actor] = &MatrixSession{
			Name:   spec.actor,
			UserID: id,
			do: func(t *testing.T, method, path string, body interface{}) *http.Response {
				return MakeAuthRequestWithToken(t, server, cookie, method, path, body)
			},
		}
	}

	// SystemAdmin: the admin user that CreateBearerToken set up. Reuse the
	// admin session cookie stashed on the test server.
	sessions[ActorSystemAdmin] = &MatrixSession{
		Name:   ActorSystemAdmin,
		UserID: 0, // admin user id not tracked on TestServer; matrix cells don't need it
		do: func(t *testing.T, method, path string, body interface{}) *http.Response {
			return MakeAuthRequest(t, server, method, path, body)
		},
	}

	// ActorPortalCustomer setup is deferred (see comment on the const).
	// When the 'Portal Customer' contact_role seed is restored, reinstate:
	//   _, portalToken := CreatePortalCustomerWithSession(t, server, ...)
	//   sessions[ActorPortalCustomer] = &MatrixSession{...MakePortalRequest...}

	return sessions
}
