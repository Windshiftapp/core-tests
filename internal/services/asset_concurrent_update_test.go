//go:build test

package services

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"windshift/internal/assetevents"
	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/testutils"
)

func TestAssetUpdateRejectsStaleSnapshotWithoutChangingDataOrEvents(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()
	userID, setID, typeID := assetEventFixture(t, db)
	repo := repository.NewAssetRepository(db)
	service := NewAssetService(db, repo)
	actor := AuditActor{UserID: userID}
	asset, err := service.CreateAsset(actor, repository.CreateAssetInput{
		SetID: setID, AssetTypeID: typeID, Title: "Original", AssetTag: "Original tag", CreatedBy: userID,
		CreatedAt: time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := repo.GetAssetUpdateSnapshot(asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	committed := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		<-committed
		_, err := service.UpdateAsset(actor, asset.ID, *snapshot, repository.UpdateAssetInput{
			AssetTypeID: typeID, Title: "Original", AssetTag: "Stale writer tag", StatusID: asset.StatusID,
		}, nil)
		result <- err
	}()
	_, writeErr := service.UpdateAsset(actor, asset.ID, *snapshot, repository.UpdateAssetInput{
		AssetTypeID: typeID, Title: "Committed title", AssetTag: "Original tag", StatusID: asset.StatusID,
	}, nil)
	close(committed)
	staleErr := <-result
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	if !errors.Is(staleErr, ErrAssetVersionConflict) {
		t.Errorf("stale update error = %v; want ErrAssetVersionConflict", staleErr)
	}
	row, err := repo.FindAssetFullByID(asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	current := repository.AssetRowToModel(*row)
	if current.Title != "Committed title" || current.AssetTag != "Original tag" {
		t.Errorf("persisted title/tag = %q/%q, want committed title/original tag", current.Title, current.AssetTag)
	}
	var updates int
	if err := db.QueryRow("SELECT COUNT(*) FROM domain_events WHERE aggregate_type = 'asset' AND aggregate_id = ? AND event_type = 'asset.updated.v1'", asset.ID).Scan(&updates); err != nil {
		t.Fatal(err)
	}
	if updates != 1 {
		t.Errorf("updated events = %d, want 1", updates)
	}
}

func TestAssetStatusUpdateRejectsStaleTransition(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	t.Cleanup(func() { _ = tdb.Close() })
	db := tdb.GetDatabase()
	userID, setID, typeID := assetEventFixture(t, db)
	repo := repository.NewAssetRepository(db)
	service := NewAssetService(db, repo)
	actor := AuditActor{UserID: userID}
	var statuses []int
	for _, name := range []string{"Original", "Committed", "Stale"} {
		id, err := repo.CreateAssetStatusTransactional(&models.AssetStatus{SetID: setID, Name: name, Color: "#000000"})
		if err != nil {
			t.Fatal(err)
		}
		statuses = append(statuses, id)
	}
	asset, err := service.CreateAsset(actor, repository.CreateAssetInput{
		SetID: setID, AssetTypeID: typeID, Title: "Status race", StatusID: &statuses[0], CreatedBy: userID,
		CreatedAt: time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := repo.GetAssetUpdateSnapshot(asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		_, err := service.UpdateAsset(actor, asset.ID, *snapshot, repository.UpdateAssetInput{
			AssetTypeID: typeID, Title: asset.Title, StatusID: &statuses[i],
		}, nil)
		if i == 1 && err != nil {
			t.Fatal(err)
		}
		if i == 2 && !errors.Is(err, ErrAssetVersionConflict) {
			t.Fatalf("stale status error = %v", err)
		}
	}
	row, err := repo.FindAssetFullByID(asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !row.StatusID.Valid || int(row.StatusID.Int64) != statuses[1] {
		t.Fatalf("persisted status = %v, want %d", row.StatusID, statuses[1])
	}
	rows, err := db.Query("SELECT payload FROM domain_events WHERE aggregate_type = 'asset' AND aggregate_id = ? AND event_type = ?", asset.ID, assetevents.StatusChanged)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var count int
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var event assetevents.StatusChangedV1
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			t.Fatal(err)
		}
		if event.OldStatusID == nil || *event.OldStatusID != statuses[0] || event.NewStatusID == nil || *event.NewStatusID != statuses[1] {
			t.Fatalf("incorrect persisted transition: %+v", event)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("status events = %d, want 1", count)
	}
}
