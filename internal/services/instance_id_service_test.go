package services

import (
	"testing"

	"windshift/internal/repository"
	"windshift/internal/testutils"
)

// The instance ID identifies this install to the licensing portal: it must be
// stable across calls and restarts, formatted for humans, and carry enough
// entropy that regenerating IDs until one matches a stolen license stays
// infeasible.
func TestInstanceService_GetOrCreateIsStableAndWellFormed(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()

	service := NewInstanceService(repository.NewSystemSettingRepository(tdb.Database))

	first, err := service.GetOrCreate()
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	second, err := service.GetOrCreate()
	if err != nil {
		t.Fatalf("GetOrCreate (second call): %v", err)
	}
	if first != second {
		t.Errorf("instance id changed between calls: %q vs %q", first, second)
	}

	if len(first) != 19 || first[4] != '-' || first[9] != '-' || first[14] != '-' {
		t.Errorf("instance id %q is not formatted XXXX-XXXX-XXXX-XXXX", first)
	}
}
