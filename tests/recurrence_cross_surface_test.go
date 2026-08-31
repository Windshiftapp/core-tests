package tests

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/repository"
	"windshift/internal/restapi"
	"windshift/internal/services"
)

func TestRecurrence_CookieAndRESTV1Contract(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Recurrence Contract", shortKey("REC"))
	LockDownWorkspace(t, server, workspaceID)

	t.Run("create applies equivalent shared behavior", func(t *testing.T) {
		cookieItemID := CreateTestItem(t, server, workspaceID, "Cookie recurrence template")
		v1ItemID := CreateTestItem(t, server, workspaceID, "V1 recurrence template")
		body := map[string]interface{}{
			"rrule":   "FREQ=WEEKLY;COUNT=4",
			"dtstart": "2026-08-01",
		}

		cookieResponse := MakeAuthRequest(t, server, http.MethodPost,
			fmt.Sprintf("/items/%d/recurrence", cookieItemID), body)
		defer cookieResponse.Body.Close()
		AssertStatusCode(t, cookieResponse, http.StatusCreated)
		var cookieRule models.RecurrenceRule
		DecodeJSON(t, cookieResponse, &cookieRule)

		v1Response := MakeBearerRequest(t, server, http.MethodPost,
			fmt.Sprintf("/rest/api/v1/items/%d/recurrence", v1ItemID), body)
		defer v1Response.Body.Close()
		AssertStatusCode(t, v1Response, http.StatusCreated)
		var v1Rule models.RecurrenceRule
		DecodeJSON(t, v1Response, &v1Rule)

		if cookieRule.RRule != v1Rule.RRule || !cookieRule.DtStart.Equal(v1Rule.DtStart) ||
			cookieRule.Timezone != v1Rule.Timezone || cookieRule.LeadTimeDays != v1Rule.LeadTimeDays ||
			cookieRule.CopyAssignee != v1Rule.CopyAssignee || cookieRule.CopyPriority != v1Rule.CopyPriority ||
			cookieRule.CopyCustomFields != v1Rule.CopyCustomFields || cookieRule.CopyDescription != v1Rule.CopyDescription ||
			cookieRule.IsActive != v1Rule.IsActive {
			t.Fatalf("shared recurrence fields differ: cookie=%+v v1=%+v", cookieRule, v1Rule)
		}
	})

	t.Run("validation decisions match while envelopes remain surface-specific", func(t *testing.T) {
		body := map[string]interface{}{"rrule": "NOT_AN_RRULE", "dtstart": "2026-08-01"}
		cookieResponse := MakeAuthRequest(t, server, http.MethodPost, "/recurrence-rules/preview", body)
		defer cookieResponse.Body.Close()
		AssertStatusCode(t, cookieResponse, http.StatusBadRequest)

		v1Response := MakeBearerRequest(t, server, http.MethodPost, "/rest/api/v1/recurrence-rules/preview", body)
		defer v1Response.Body.Close()
		AssertStatusCode(t, v1Response, http.StatusBadRequest)
	})

	t.Run("edit denial matches and does not create a rule", func(t *testing.T) {
		itemID := CreateTestItem(t, server, workspaceID, "Denied recurrence template")
		viewerID, username, password := CreateTestUserWithCredentials(t, server, "recurrence_viewer", "recurrence_viewer@test.com")
		AssignWorkspaceRole(t, server, viewerID, workspaceID, "Viewer")
		cookie := CreateBearerTokenForUser(t, server, username, password)
		bearer := createTokenWithScopesAsUser(t, server, username, password, []string{"items:read", "items:write"})
		body := map[string]interface{}{"rrule": "FREQ=DAILY;COUNT=2", "dtstart": "2026-08-01"}

		cookieResponse := MakeAuthRequestWithToken(t, server, cookie, http.MethodPost,
			fmt.Sprintf("/items/%d/recurrence", itemID), body)
		defer cookieResponse.Body.Close()
		AssertStatusCode(t, cookieResponse, http.StatusNotFound)

		v1Response := MakeBearerRequestWithToken(t, server, bearer, http.MethodPost,
			fmt.Sprintf("/rest/api/v1/items/%d/recurrence", itemID), body)
		defer v1Response.Body.Close()
		AssertStatusCode(t, v1Response, http.StatusNotFound)

		getResponse := MakeAuthRequest(t, server, http.MethodGet, fmt.Sprintf("/items/%d/recurrence", itemID), nil)
		defer getResponse.Body.Close()
		AssertStatusCode(t, getResponse, http.StatusOK)
		var rule *models.RecurrenceRule
		DecodeJSON(t, getResponse, &rule)
		if rule != nil {
			t.Fatalf("denied create persisted recurrence: %+v", rule)
		}
	})

	t.Run("workspace rule quota returns conflict on both surfaces", func(t *testing.T) {
		cookieItemID := CreateTestItem(t, server, workspaceID, "Cookie quota candidate")
		v1ItemID := CreateTestItem(t, server, workspaceID, "V1 quota candidate")
		db := server.server.DB()
		itemRepo := repository.NewItemRepository(db)
		recurrenceRepo := repository.NewRecurrenceRepository(db)

		existingCount, err := recurrenceRepo.CountByWorkspace(workspaceID)
		if err != nil {
			t.Fatalf("count existing recurrence rules: %v", err)
		}
		for i := existingCount; i < services.MaxRecurrenceRulesPerWorkspace; i++ {
			tx, beginErr := db.Begin()
			if beginErr != nil {
				t.Fatalf("begin quota seed transaction %d: %v", i, beginErr)
			}
			nextNumber, numberErr := itemRepo.GetNextWorkspaceItemNumber(tx, workspaceID)
			if numberErr != nil {
				_ = tx.Rollback()
				t.Fatalf("next workspace item number %d: %v", i, numberErr)
			}
			itemID, createErr := itemRepo.Create(tx, &models.Item{
				WorkspaceID:         workspaceID,
				WorkspaceItemNumber: nextNumber,
				Title:               fmt.Sprintf("Quota recurrence template %d", i+1),
			})
			if createErr != nil {
				_ = tx.Rollback()
				t.Fatalf("create quota item %d: %v", i, createErr)
			}
			if commitErr := tx.Commit(); commitErr != nil {
				t.Fatalf("commit quota item %d: %v", i, commitErr)
			}
			if _, createErr := recurrenceRepo.Create(&models.RecurrenceRule{
				TemplateItemID:   itemID,
				WorkspaceID:      workspaceID,
				RRule:            "FREQ=DAILY;COUNT=2",
				DtStart:          time.Date(2026, time.August, 1, 0, 0, 0, 0, time.UTC),
				Timezone:         "UTC",
				LeadTimeDays:     14,
				CopyAssignee:     true,
				CopyPriority:     true,
				CopyCustomFields: true,
				CopyDescription:  true,
				IsActive:         true,
			}); createErr != nil {
				t.Fatalf("create quota recurrence rule %d: %v", i, createErr)
			}
		}

		body := map[string]interface{}{"rrule": "FREQ=DAILY;COUNT=2", "dtstart": "2026-08-01"}
		cookieResponse := MakeAuthRequest(t, server, http.MethodPost,
			fmt.Sprintf("/items/%d/recurrence", cookieItemID), body)
		defer cookieResponse.Body.Close()
		AssertStatusCode(t, cookieResponse, http.StatusConflict)
		var cookieError restapi.ErrorResponse
		DecodeJSON(t, cookieResponse, &cookieError)
		if cookieError.Code != restapi.ErrCodeConflict ||
			cookieError.Error != services.RecurrenceWorkspaceLimitMessage() {
			t.Fatalf("cookie quota error = %+v, want code %q and message %q",
				cookieError, restapi.ErrCodeConflict, services.RecurrenceWorkspaceLimitMessage())
		}

		v1Response := MakeBearerRequest(t, server, http.MethodPost,
			fmt.Sprintf("/rest/api/v1/items/%d/recurrence", v1ItemID), body)
		defer v1Response.Body.Close()
		AssertStatusCode(t, v1Response, http.StatusConflict)
		var v1Error restapi.ErrorResponse
		DecodeJSON(t, v1Response, &v1Error)
		if v1Error.Code != restapi.ErrCodeConflict ||
			v1Error.Error != services.RecurrenceWorkspaceLimitMessage() {
			t.Fatalf("REST v1 quota error = %+v, want code %q and message %q",
				v1Error, restapi.ErrCodeConflict, services.RecurrenceWorkspaceLimitMessage())
		}

		count, err := recurrenceRepo.CountByWorkspace(workspaceID)
		if err != nil || count != services.MaxRecurrenceRulesPerWorkspace {
			t.Fatalf("workspace recurrence count = %d, error = %v, want %d", count, err, services.MaxRecurrenceRulesPerWorkspace)
		}
	})
}

func TestRecurrenceVolumeDiagnosticsRequireSystemAdmin(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	adminToken := CreateBearerToken(t, server)

	adminResponse := MakeAuthRequestWithToken(
		t,
		server,
		adminToken,
		http.MethodGet,
		"/admin/diagnostics/recurrence-volume",
		nil,
	)
	defer adminResponse.Body.Close()
	AssertStatusCode(t, adminResponse, http.StatusOK)
	var before map[string]interface{}
	DecodeJSON(t, adminResponse, &before)

	_, username, password := CreateTestUserWithCredentials(
		t,
		server,
		"recurrence_diagnostics_user",
		"recurrence-diagnostics-user@test.com",
	)
	nonAdminToken := CreateBearerTokenForUser(t, server, username, password)

	getResponse := MakeAuthRequestWithToken(
		t,
		server,
		nonAdminToken,
		http.MethodGet,
		"/admin/diagnostics/recurrence-volume",
		nil,
	)
	defer getResponse.Body.Close()
	AssertStatusCode(t, getResponse, http.StatusForbidden)
	var getError restapi.ErrorResponse
	DecodeJSON(t, getResponse, &getError)
	if getError.Code != restapi.ErrCodeInsufficientPermission {
		t.Fatalf("non-admin diagnostics GET error = %+v, want code %q",
			getError, restapi.ErrCodeInsufficientPermission)
	}

	updateResponse := MakeAuthRequestWithToken(
		t,
		server,
		nonAdminToken,
		http.MethodPut,
		"/admin/diagnostics/recurrence-volume",
		map[string]interface{}{
			"diagnostic_enabled": false,
			"warning_threshold":  1,
		},
	)
	defer updateResponse.Body.Close()
	AssertStatusCode(t, updateResponse, http.StatusForbidden)
	var updateError restapi.ErrorResponse
	DecodeJSON(t, updateResponse, &updateError)
	if updateError.Code != restapi.ErrCodeInsufficientPermission {
		t.Fatalf("non-admin diagnostics PUT error = %+v, want code %q",
			updateError, restapi.ErrCodeInsufficientPermission)
	}

	afterResponse := MakeAuthRequestWithToken(
		t,
		server,
		adminToken,
		http.MethodGet,
		"/admin/diagnostics/recurrence-volume",
		nil,
	)
	defer afterResponse.Body.Close()
	AssertStatusCode(t, afterResponse, http.StatusOK)
	var after map[string]interface{}
	DecodeJSON(t, afterResponse, &after)
	if after["diagnostic_enabled"] != before["diagnostic_enabled"] ||
		after["warning_threshold"] != before["warning_threshold"] {
		t.Fatalf("unauthorized diagnostics update changed settings: before=%v after=%v", before, after)
	}
}
