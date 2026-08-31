//go:build test

package handlers

import (
	"net/http"
	"testing"

	"windshift/internal/logger"
	"windshift/internal/models"
	"windshift/internal/services"
	"windshift/internal/testutils"
)

func TestIterationHandlerUpdateDistinguishesOmittedEmptyAndNull(t *testing.T) {
	tdb := testutils.CreateTestDB(t, true)
	defer tdb.Close()
	db := tdb.GetDatabase()
	permService := createIterationTestServices(t, *tdb)
	planning := services.NewPlanningService(db)
	handler := NewIterationHandler(planning, permService, logger.NewAuditor(db))

	var typeID int
	if err := db.QueryRow(
		"INSERT INTO iteration_types (name, color) VALUES (?, ?) RETURNING id",
		"Patch type", "#123456",
	).Scan(&typeID); err != nil {
		t.Fatalf("insert iteration type: %v", err)
	}
	iteration, err := planning.CreateIteration(services.CreateIterationParams{
		Name:        "Original",
		Description: "Keep me",
		StartDate:   "2026-07-01",
		EndDate:     "2026-07-14",
		Status:      "planned",
		TypeID:      &typeID,
		IsGlobal:    true,
	})
	if err != nil {
		t.Fatalf("CreateIteration: %v", err)
	}

	update := func(body map[string]interface{}) models.Iteration {
		t.Helper()
		req := testutils.CreateJSONRequest(t, http.MethodPut, "/api/global/iterations/1", body)
		req.SetPathValue("id", testutils.IntToString(iteration.ID))
		rr := testutils.ExecuteAuthenticatedRequest(t, handler.Update, req, nil)
		rr.AssertStatusCode(http.StatusOK)
		var response models.Iteration
		rr.AssertJSONResponse(&response)
		return response
	}

	renamed := update(map[string]interface{}{"name": "Renamed"})
	if renamed.Description != "Keep me" || renamed.TypeID == nil || *renamed.TypeID != typeID {
		t.Fatalf("omitted fields were not preserved: %+v", renamed)
	}
	clearedDescription := update(map[string]interface{}{"description": ""})
	if clearedDescription.Description != "" || clearedDescription.TypeID == nil {
		t.Fatalf("empty description clear failed: %+v", clearedDescription)
	}
	clearedType := update(map[string]interface{}{"type_id": nil})
	if clearedType.TypeID != nil || clearedType.Name != "Renamed" {
		t.Fatalf("null type clear failed: %+v", clearedType)
	}
}
