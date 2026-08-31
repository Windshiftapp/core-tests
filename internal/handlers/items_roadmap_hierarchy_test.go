//go:build test

package handlers

import (
	"errors"
	"reflect"
	"testing"
)

func TestAuthorizedRoadmapHierarchyRootIDsFiltersBeforeExpansion(t *testing.T) {
	workspaceIDs := map[int]int{
		1: 10,
		2: 20,
		3: 10,
		4: 20,
	}
	var checked []int

	got, err := authorizedRoadmapHierarchyRootIDs([]int{1, 2, 2, -1, 3, 4, 999}, workspaceIDs, func(workspaceID int) (bool, error) {
		checked = append(checked, workspaceID)
		return workspaceID == 20, nil
	})
	if err != nil {
		t.Fatalf("authorizedRoadmapHierarchyRootIDs: %v", err)
	}
	if want := []int{2, 4}; !reflect.DeepEqual(got, want) {
		t.Fatalf("authorized roots = %v, want %v", got, want)
	}
	if want := []int{10, 20}; !reflect.DeepEqual(checked, want) {
		t.Fatalf("permission checks = %v, want one per workspace in order %v", checked, want)
	}
}

func TestAuthorizedRoadmapHierarchyRootIDsReturnsPermissionError(t *testing.T) {
	wantErr := errors.New("permission lookup failed")
	got, err := authorizedRoadmapHierarchyRootIDs([]int{1}, map[int]int{1: 10}, func(int) (bool, error) {
		return false, wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	if got != nil {
		t.Fatalf("authorized roots = %v, want nil after permission error", got)
	}
}
