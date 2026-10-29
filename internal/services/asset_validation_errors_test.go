//go:build test

package services

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"windshift/internal/database"
	"windshift/internal/repository"
)

type failedAssetTaxonomyDB struct{ database.Database }

func (db failedAssetTaxonomyDB) QueryRow(query string, args ...any) *sql.Row {
	if strings.Contains(query, "SELECT EXISTS(SELECT 1 FROM asset_types") {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return db.QueryRowContext(ctx, query, args...)
	}
	return db.Database.QueryRow(query, args...)
}

func TestAssetValidationPreservesDatabaseFailure(t *testing.T) {
	app, db, userID, setID, typeID := assetGraphFixture(t)
	failedDB := failedAssetTaxonomyDB{Database: db}
	app.repo = repository.NewAssetRepository(failedDB)
	app.assets = NewAssetService(failedDB, app.repo)
	_, err := app.CreateAsset(userID, setID, AuditActor{UserID: userID}, AssetMutationInput{AssetTypeID: typeID, Title: "Must not persist"})
	var validation *AssetValidationError
	if !errors.Is(err, context.Canceled) || errors.As(err, &validation) {
		t.Fatalf("database failure was classified as %T: %v", err, err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM assets").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("persisted assets = %d", count)
	}
}
