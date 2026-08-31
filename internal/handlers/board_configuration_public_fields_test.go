//go:build test

package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"windshift/internal/models"
)

func TestBoardConfigurationRejectsUnapprovedFieldsForPublicCollection(t *testing.T) {
	const userID = 1
	db := newNegativeTestDB(t)
	seedNegativeTestUser(t, db, userID)
	if _, err := db.Exec(`
		INSERT INTO collections (id, name, ql_query, is_public, created_by)
		VALUES (42, 'Public field contract', '', TRUE, ?)
	`, userID); err != nil {
		t.Fatalf("seed public collection: %v", err)
	}
	handler := newNegativeBoardConfigurationHandler(db, newNegativeTestPermissionService(t, db))
	request := authedRequest(http.MethodPost, "/collections/42/board-configuration", userID, models.BoardConfigurationRequest{
		CardFields: []models.ListColumn{{
			FieldIdentifier: "custom_field_7",
			FieldType:       "custom",
		}},
	})
	request.SetPathValue("id", "42")
	recorder := httptest.NewRecorder()

	handler.CreateForCollection(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM board_configurations WHERE collection_id = 42`).Scan(&count); err != nil {
		t.Fatalf("count board configurations: %v", err)
	}
	if count != 0 {
		t.Fatalf("board configuration count = %d, want 0 after rejection", count)
	}
}

func TestBoardConfigurationAcceptsSupportedFieldsForPublicCollection(t *testing.T) {
	const userID = 1
	db := newNegativeTestDB(t)
	seedNegativeTestUser(t, db, userID)
	if _, err := db.Exec(`
		INSERT INTO collections (id, name, ql_query, is_public, created_by)
		VALUES (42, 'Public field contract', '', TRUE, ?)
	`, userID); err != nil {
		t.Fatalf("seed public collection: %v", err)
	}
	handler := newNegativeBoardConfigurationHandler(db, newNegativeTestPermissionService(t, db))
	request := authedRequest(http.MethodPost, "/collections/42/board-configuration", userID, models.BoardConfigurationRequest{
		CardFields: []models.ListColumn{{
			FieldIdentifier: "status",
			FieldType:       "system",
		}},
	})
	request.SetPathValue("id", "42")
	recorder := httptest.NewRecorder()

	handler.CreateForCollection(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", recorder.Code, recorder.Body.String())
	}
}
