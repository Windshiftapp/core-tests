//go:build test

package services

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

type observedGraphDB struct {
	database.Database
	queries atomic.Int64
	started chan struct{}
}

func (db *observedGraphDB) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	db.queries.Add(1)
	if db.started != nil && strings.Contains(q, "FROM item_links") {
		close(db.started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return db.Database.QueryContext(ctx, q, args...)
}
func (db *observedGraphDB) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	if ctx.Err() == nil {
		db.queries.Add(1)
	}
	return db.Database.QueryRowContext(ctx, q, args...)
}

func assetGraphFixture(t *testing.T) (*AssetApplicationService, *observedGraphDB, int, int, int) {
	t.Helper()
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := &observedGraphDB{Database: tdb.GetDatabase()}
	userID, setID, typeID := assetEventFixture(t, db)
	repo := repository.NewAssetRepository(db)
	roleID, err := repo.GetAssetRoleIDByName("Administrator")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.AssignUserRole(setID, userID, roleID, userID); err != nil {
		t.Fatal(err)
	}
	config := DefaultPermissionCacheConfig()
	config.WarmupOnStartup = false
	perms, err := NewPermissionService(db, config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = perms.Close() })
	return NewAssetApplicationService(db, perms, nil, nil), db, userID, setID, typeID
}

func TestAssetGraphCancelsAnInFlightQuery(t *testing.T) {
	app, db, userID, setID, typeID := assetGraphFixture(t)
	asset, err := app.CreateAsset(userID, setID, AuditActor{UserID: userID}, AssetMutationInput{AssetTypeID: typeID, Title: "Origin"})
	if err != nil {
		t.Fatal(err)
	}
	db.started = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := app.RelationshipGraph(ctx, userID, asset.ID); done <- err }()
	select {
	case <-db.started:
	case <-time.After(5 * time.Second):
		t.Fatal("graph did not start query")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation = %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("graph ignored cancellation")
	}
}

func TestAssetGraphBoundsQueriesAcrossManyFields(t *testing.T) {
	app, db, userID, setID, typeID := assetGraphFixture(t)
	// These schema fixtures exercise the read budget, without triggering mutations under test.
	for i := 0; i < 50; i++ {
		if _, err := db.ExecWrite("INSERT INTO custom_field_definitions (name, field_type) VALUES (?, 'asset')", fmt.Sprintf("Reference %d", i)); err != nil {
			t.Fatal(err)
		}
	}
	var origin int
	for i := 0; i < 5; i++ {
		asset, err := app.CreateAsset(userID, setID, AuditActor{UserID: userID}, AssetMutationInput{AssetTypeID: typeID, Title: fmt.Sprint(i)})
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			origin = asset.ID
			continue
		}
		if _, err := db.ExecWrite("INSERT INTO item_links (link_type_id, source_type, source_id, target_type, target_id) VALUES (4, 'asset', ?, 'asset', ?)", origin, asset.ID); err != nil {
			t.Fatal(err)
		}
	}
	db.queries.Store(0)
	graph, err := app.RelationshipGraph(context.Background(), userID, origin)
	if err != nil {
		t.Fatal(err)
	}
	if !graph.Truncated || len(graph.Nodes) != 5 {
		t.Fatalf("graph = %+v", graph)
	}
	if queries := db.queries.Load(); queries != assetGraphMaxQueries {
		t.Fatalf("queries = %d, want %d", queries, assetGraphMaxQueries)
	}
}

func TestAssetGraphStopsAtTwoHops(t *testing.T) {
	app, db, userID, setID, typeID := assetGraphFixture(t)
	ids := make([]int, 4)
	for i := range ids {
		asset, err := app.CreateAsset(userID, setID, AuditActor{UserID: userID}, AssetMutationInput{AssetTypeID: typeID, Title: fmt.Sprint(i)})
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = asset.ID
		if i > 0 {
			if _, err := db.ExecWrite("INSERT INTO item_links (link_type_id, source_type, source_id, target_type, target_id) VALUES (4, 'asset', ?, 'asset', ?)", ids[i-1], ids[i]); err != nil {
				t.Fatal(err)
			}
		}
	}
	graph, err := app.RelationshipGraph(context.Background(), userID, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	hops := map[int]int{}
	for _, node := range graph.Nodes {
		hops[node.EntityID] = node.Hop
	}
	if len(hops) != 3 || hops[ids[0]] != 0 || hops[ids[1]] != 1 || hops[ids[2]] != 2 {
		t.Fatalf("hops = %v", hops)
	}
	if _, exists := hops[ids[3]]; exists {
		t.Fatal("third-hop node was included")
	}
	if graph.Truncated {
		t.Fatal("two-hop graph unexpectedly truncated")
	}
}

func TestAssetGraphFindsCustomFieldReferences(t *testing.T) {
	app, db, userID, setID, typeID := assetGraphFixture(t)
	var fieldID int
	if err := db.QueryRow("INSERT INTO custom_field_definitions (name, field_type) VALUES ('Graph ref', 'asset') RETURNING id").Scan(&fieldID); err != nil {
		t.Fatal(err)
	}
	if err := app.repo.ReplaceAssetTypeFields(typeID, []repository.AssetTypeFieldAssignment{{CustomFieldID: fieldID}}); err != nil {
		t.Fatal(err)
	}
	origin, err := app.CreateAsset(userID, setID, AuditActor{UserID: userID}, AssetMutationInput{AssetTypeID: typeID, Title: "Origin"})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{origin.ID, map[string]any{"id": origin.ID}, []any{origin.ID, map[string]any{"id": origin.ID}}} {
		_, err := app.CreateAsset(userID, setID, AuditActor{UserID: userID}, AssetMutationInput{AssetTypeID: typeID, Title: "Referrer", CustomFieldValues: map[string]any{fmt.Sprint(fieldID): value}})
		if err != nil {
			t.Fatal(err)
		}
	}
	graph, err := app.RelationshipGraph(context.Background(), userID, origin.ID)
	if err != nil {
		t.Fatal(err)
	}
	if graph.Truncated || len(graph.Nodes) != 4 || len(graph.Edges) != 3 {
		t.Fatalf("graph = %+v", graph)
	}
	for _, edge := range graph.Edges {
		if edge.Target != fmt.Sprintf("asset-%d", origin.ID) || edge.Label != "Field: Graph ref" || edge.EdgeType != "field_reference" {
			t.Fatalf("edge = %+v", edge)
		}
	}
}
