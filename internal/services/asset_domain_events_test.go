//go:build test

package services

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"windshift/internal/assetevents"
	"windshift/internal/database"
	"windshift/internal/events"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

type rejectingAssetRecorder struct {
	assetEventRecorder
	err error
}

func (r rejectingAssetRecorder) Created(context.Context, database.Tx, assetevents.AssetSnapshot, map[string]any, assetevents.Metadata) (*events.Event, error) {
	return nil, r.err
}

func TestAssetCreateRollsBackWhenCanonicalEventAppendFails(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()
	userID, setID, typeID := assetEventFixture(t, db)
	recorderErr := errors.New("event journal unavailable")
	service := NewAssetService(db, repository.NewAssetRepository(db))
	service.eventRecorder = rejectingAssetRecorder{assetEventRecorder: service.eventRecorder, err: recorderErr}

	_, err := service.CreateAsset(AuditActor{UserID: userID}, repository.CreateAssetInput{
		SetID: setID, AssetTypeID: typeID, Title: "Must roll back", CreatedBy: userID, CreatedAt: time.Now().UTC(),
	}, nil)
	if !errors.Is(err, recorderErr) {
		t.Fatalf("CreateAsset() error = %v, want %v", err, recorderErr)
	}
	var assets, facts int
	if err := db.QueryRow("SELECT COUNT(*) FROM assets WHERE title = 'Must roll back'").Scan(&assets); err != nil {
		t.Fatalf("count rolled-back assets: %v", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM domain_events WHERE aggregate_type = 'asset'").Scan(&facts); err != nil {
		t.Fatalf("count rolled-back facts: %v", err)
	}
	if assets != 0 || facts != 0 {
		t.Fatalf("rollback left assets:%d facts:%d, want 0/0", assets, facts)
	}
}

func TestAssetMutationLifecycleRecordsOrderedCanonicalFacts(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()
	userID, setID, typeID := assetEventFixture(t, db)
	insert := func(label, query string, args ...any) int {
		t.Helper()
		var id int
		if err := db.QueryRow(query, args...).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", label, err)
		}
		return id
	}
	oldStatusID := insert("old status", "INSERT INTO asset_statuses (set_id, name) VALUES (?, 'Old') RETURNING id", setID)
	newStatusID := insert("new status", "INSERT INTO asset_statuses (set_id, name) VALUES (?, 'New') RETURNING id", setID)
	repo := repository.NewAssetRepository(db)
	service := NewAssetService(db, repo)
	asset, err := service.CreateAsset(AuditActor{UserID: userID}, repository.CreateAssetInput{
		SetID: setID, AssetTypeID: typeID, StatusID: &oldStatusID,
		Title: "Lifecycle asset", CreatedBy: userID, CreatedAt: time.Now().UTC(),
	}, nil)
	if err != nil {
		t.Fatalf("CreateAsset() error = %v", err)
	}
	snapshot, err := repo.GetAssetUpdateSnapshot(asset.ID)
	if err != nil {
		t.Fatalf("GetAssetUpdateSnapshot() error = %v", err)
	}
	if _, err := service.UpdateAsset(AuditActor{UserID: userID}, asset.ID, *snapshot, repository.UpdateAssetInput{
		AssetTypeID: typeID, StatusID: &newStatusID, Title: "Lifecycle asset updated",
	}, nil); err != nil {
		t.Fatalf("UpdateAsset() error = %v", err)
	}
	if err := service.DeleteAsset(AuditActor{UserID: userID}, asset.ID); err != nil {
		t.Fatalf("DeleteAsset() error = %v", err)
	}

	rows, err := db.Query(`
		SELECT event_type, aggregate_sequence
		FROM domain_events
		WHERE aggregate_type = 'asset' AND aggregate_id = ?
		ORDER BY aggregate_sequence
	`, asset.ID)
	if err != nil {
		t.Fatalf("query asset facts: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var gotTypes []string
	var gotSequences []int64
	for rows.Next() {
		var eventType string
		var sequence int64
		if err := rows.Scan(&eventType, &sequence); err != nil {
			t.Fatalf("scan asset fact: %v", err)
		}
		gotTypes = append(gotTypes, eventType)
		gotSequences = append(gotSequences, sequence)
	}
	wantTypes := []string{
		assetevents.Created, DurableAssetActionCompatibilityEvent,
		assetevents.StatusChanged, DurableAssetActionCompatibilityEvent,
		assetevents.Updated, DurableAssetActionCompatibilityEvent,
		assetevents.Deleted, DurableAssetActionCompatibilityEvent,
	}
	wantSequences := []int64{1, 2, 3, 4, 5, 6, 7, 8}
	if !reflect.DeepEqual(gotTypes, wantTypes) || !reflect.DeepEqual(gotSequences, wantSequences) {
		t.Fatalf("asset facts = %v sequences %v, want %v / %v", gotTypes, gotSequences, wantTypes, wantSequences)
	}
}

func TestAssetMutationStopsCompatibilityDualWriteAfterCutover(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()
	userID, setID, typeID := assetEventFixture(t, db)
	service := NewAssetService(db, repository.NewAssetRepository(db))
	if _, err := service.compatibilityEvents().ActivateCanonicalAssets(context.Background()); err != nil {
		t.Fatalf("activate asset cutover: %v", err)
	}
	if _, err := service.CreateAsset(AuditActor{UserID: userID}, repository.CreateAssetInput{
		SetID: setID, AssetTypeID: typeID, Title: "After cutover", CreatedBy: userID, CreatedAt: time.Now().UTC(),
	}, nil); err != nil {
		t.Fatalf("CreateAsset() error = %v", err)
	}
	var canonical, compatibility int
	if err := db.QueryRow("SELECT COUNT(*) FROM domain_events WHERE event_type = ?", assetevents.Created).Scan(&canonical); err != nil {
		t.Fatalf("count canonical facts: %v", err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM domain_events WHERE event_type = ?", DurableAssetActionCompatibilityEvent).Scan(&compatibility); err != nil {
		t.Fatalf("count compatibility facts: %v", err)
	}
	if canonical != 1 || compatibility != 0 {
		t.Fatalf("post-cutover facts = canonical:%d compatibility:%d, want 1/0", canonical, compatibility)
	}
}

func assetEventFixture(t *testing.T, db database.Database) (userID, setID, typeID int) {
	t.Helper()
	insert := func(label, query string, args ...any) int {
		t.Helper()
		var id int
		if err := db.QueryRow(query, args...).Scan(&id); err != nil {
			t.Fatalf("insert %s: %v", label, err)
		}
		return id
	}
	userID = insert("user", "INSERT INTO users (email, username, first_name, last_name) VALUES ('asset-events@example.test', 'asset-events', 'Asset', 'Events') RETURNING id")
	setID = insert("asset set", "INSERT INTO asset_management_sets (name, created_by) VALUES ('Event assets', ?) RETURNING id", userID)
	typeID = insert("asset type", "INSERT INTO asset_types (set_id, name) VALUES (?, 'Server') RETURNING id", setID)
	return userID, setID, typeID
}
