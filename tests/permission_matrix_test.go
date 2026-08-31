package tests

import "testing"

// TestPermissionMatrix asserts the (actor × permission-class) status matrix
// for the API surface. For each PermissionClass in Classes, the test:
//
//  1. Looks up the representative MatrixRoute for that class.
//  2. Walks every MatrixActor in deterministic order (MatrixActors).
//  3. Fires the route via that actor's session.
//  4. Asserts the response is *exactly* class.Expected[actor] — not
//     "rejected" (401/403/404) blanket-acceptance. The point of this test
//     is to catch drift from the security-policy codes documented in
//     matrix_classes.go.
//
// Why this exists: permission tests today are distributed by feature as
// one-off t.Run subtests; there's no single place a reviewer can see "for
// every route, here is the expected status for every actor." A new route
// can land with the wrong status code (e.g. 403 instead of the
// 404-for-existence-privacy invariant) and no test catches it. This is
// that catch.
//
// Seeded with workspace.item.view (GET /items/{id}). Subsequent commits
// expand Classes and MatrixRoutes one resource family at a time.
func TestPermissionMatrix(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	// Two workspaces: target is what we test against; other gives
	// ActorCrossWorkspaceMember a legitimate role somewhere so the
	// rejection on target isn't "user has no role anywhere."
	targetWS, targetKey := CreateTestWorkspace(t, server, "Matrix Target", shortKey("MTRX"))
	otherWS, _ := CreateTestWorkspace(t, server, "Matrix Other", shortKey("MTRO"))

	// Lock down the target so that workspace-permission denial actually
	// manifests as 404 (the open-by-default behaviour would let
	// ActorNoMembership see the item, defeating the matrix).
	LockDownWorkspace(t, server, targetWS)

	// One item per workspace so the GET /items/{id} representative has
	// a concrete ID to target.
	targetItem := CreateTestItem(t, server, targetWS, "Matrix target item")
	otherItem := CreateTestItem(t, server, otherWS, "Matrix other item")

	actors := SetupMatrixActors(t, server, MatrixSubjects{
		TargetWorkspaceID:  targetWS,
		TargetWorkspaceKey: targetKey,
		OtherWorkspaceID:   otherWS,
	})

	fixtures := MatrixFixtures{
		TargetWorkspaceID: targetWS,
		OtherWorkspaceID:  otherWS,
		TargetItemID:      targetItem,
		OtherItemID:       otherItem,
	}

	for _, class := range Classes {
		t.Run(class.Name, func(t *testing.T) {
			route := RepresentativeRouteFor(class.Name)
			if route == nil {
				t.Fatalf("no MatrixRoute classified into %q — add one to MatrixRoutes in matrix_routes.go", class.Name)
			}

			for _, actorName := range MatrixActors {
				expected, ok := class.Expected[actorName]
				if !ok {
					t.Errorf("class %q has no Expected entry for actor %q — every class must classify every actor", class.Name, actorName)
					continue
				}
				session, ok := actors[actorName]
				if !ok {
					t.Fatalf("SetupMatrixActors did not produce session for %q", actorName)
				}

				t.Run(string(actorName), func(t *testing.T) {
					path := ExpandMatrixPath(route.Path, fixtures)
					var body any
					if route.Body != nil {
						body = route.Body(fixtures)
					}
					resp := session.Do(t, route.Method, path, body)
					defer resp.Body.Close()

					if resp.StatusCode != expected {
						t.Errorf("%s %s as %s: expected %d, got %d (class=%s)",
							route.Method, path, actorName, expected, resp.StatusCode, class.Name)
						// Surface body on mismatch for easier debugging.
						AssertStatusCode(t, resp, expected)
					}
				})
			}
		})
	}

	// Final coverage sanity: every class in Classes must also have a
	// representative MatrixRoute. The loop above already fails for missing
	// routes; this is a more obvious "missing class" message in case the
	// list grows out of sync.
	for _, class := range Classes {
		if RepresentativeRouteFor(class.Name) == nil {
			t.Errorf("PermissionClass %q has no representative MatrixRoute", class.Name)
		}
	}
}
