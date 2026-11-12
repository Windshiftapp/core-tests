//go:build test

package handlers

import (
	"context"
	"encoding/json"
	"testing"

	"windshift/internal/blobstore"
	"windshift/internal/jira"
)

type unavailableAssetsClient struct {
	jira.Client
}

func (c *unavailableAssetsClient) ListObjectSchemas(context.Context) ([]jira.AssetObjectSchema, error) {
	return nil, jira.ErrAssetsNotAvailable
}

// TestImportJiraAssetsUnavailableRecordsFindingWithoutCreatingSets is the
// WI-2078 regression: an unsupported Assets capability must not create empty
// native asset sets, and must record a durable finding.
func TestImportJiraAssetsUnavailableRecordsFindingWithoutCreatingSets(t *testing.T) {
	db := newJiraServiceManagementTestDB(t)
	h := NewJiraImportHandler(db, "jira-assets-unavailable-secret", "", blobstore.Disabled())

	h.importJiraAssets(context.Background(), "job-jsm", &unavailableAssetsClient{}, 0)

	var setCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM asset_management_sets`).Scan(&setCount); err != nil {
		t.Fatalf("count asset sets: %v", err)
	}
	if setCount != 0 {
		t.Fatalf("asset set count = %d, want 0 when Assets is unavailable", setCount)
	}

	var metadataJSON string
	if err := db.QueryRow(`
		SELECT metadata_json FROM jira_import_id_mappings
		WHERE job_id = 'job-jsm' AND entity_type = 'fidelity_finding' AND jira_id = 'assets-capability'
	`).Scan(&metadataJSON); err != nil {
		t.Fatalf("load Assets fidelity finding: %v", err)
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(metadataJSON), &metadata); err != nil {
		t.Fatalf("decode Assets fidelity finding: %v", err)
	}
	if metadata["code"] != "jira_assets_unavailable" || metadata["severity"] != "warning" {
		t.Fatalf("Assets finding metadata = %#v, want jira_assets_unavailable/warning", metadata)
	}
}
