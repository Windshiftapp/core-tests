package tests

import (
	"net/http"
	"testing"

	"windshift/internal/models"
)

func TestClearNotificationsPersistsAcrossReload(t *testing.T) {
	server, cleanup := StartTestServer(t, GetDBType())
	defer cleanup()
	CreateBearerToken(t, server)

	for _, title := range []string{"First", "Second"} {
		response := MakeAuthRequest(t, server, http.MethodPost, "/notifications", map[string]any{
			"title":   title,
			"message": "Persistent notification",
			"type":    "info",
		})
		AssertStatusCode(t, response, http.StatusCreated)
		response.Body.Close()
	}

	unauthenticated := makeSessionRequest(t, http.MethodDelete, server.APIBase+"/notifications", "", nil, nil)
	AssertStatusCode(t, unauthenticated, http.StatusUnauthorized)
	unauthenticated.Body.Close()

	clearResponse := MakeAuthRequest(t, server, http.MethodDelete, "/notifications", nil)
	AssertStatusCode(t, clearResponse, http.StatusNoContent)
	clearResponse.Body.Close()

	reloadResponse := MakeAuthRequest(t, server, http.MethodGet, "/notifications", nil)
	defer reloadResponse.Body.Close()
	AssertStatusCode(t, reloadResponse, http.StatusOK)

	var notifications []models.Notification
	DecodeJSON(t, reloadResponse, &notifications)
	if len(notifications) != 0 {
		t.Fatalf("notifications after clear and reload = %d, want 0", len(notifications))
	}
}
