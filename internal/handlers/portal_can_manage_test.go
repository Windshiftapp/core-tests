//go:build test

package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"windshift/internal/auth"
	"windshift/internal/blobstore"
	"windshift/internal/middleware"
	"windshift/internal/services"
)

// portalBootstrapRequestWithInternalUser builds a bootstrap request carrying
// an internal-user session for userID.
func portalBootstrapRequestWithInternalUser(userID int) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/api/portal/support/bootstrap", nil)
	request.SetPathValue("slug", "support")
	ctx := context.WithValue(request.Context(), middleware.ContextKeySession, &auth.Session{UserID: userID})
	return request.WithContext(ctx)
}

// portalBootstrapRequestWithCustomer builds a bootstrap request carrying a
// portal-customer identity.
func portalBootstrapRequestWithCustomer(customerID int) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/api/portal/support/bootstrap", nil)
	request.SetPathValue("slug", "support")
	ctx := context.WithValue(request.Context(), middleware.ContextKeyPortalCustomerID, customerID)
	return request.WithContext(ctx)
}

func decodeBootstrapPortal(t *testing.T, handler *PortalHandler, request *http.Request) map[string]any {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.GetBootstrap(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var response PortalBootstrapResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return response.Portal
}

// TestPortalBootstrapCanManageTrueForChannelManager guards the fix for the
// customize UI being shown to every internal user. A channel manager must
// receive can_manage=true so the portal offers the customization controls.
func TestPortalBootstrapCanManageTrueForChannelManager(t *testing.T) {
	db, channelID, _ := setupPortalBootstrapTest(t)
	if _, err := db.ExecWrite(`
		INSERT INTO channel_managers (channel_id, manager_type, manager_id)
		VALUES (?, 'user', 1)
	`, channelID); err != nil {
		t.Fatalf("insert channel manager: %v", err)
	}

	handler := NewPortalHandler(db, nil, nil, nil, blobstore.Disabled())
	handler.SetChannelService(services.NewChannelService(db, nil))

	portal := decodeBootstrapPortal(t, handler, portalBootstrapRequestWithInternalUser(1))
	if portal["can_manage"] != true {
		t.Fatalf("can_manage = %v, want true for channel manager", portal["can_manage"])
	}
}

// TestPortalBootstrapCanManageFalseForNonManager guards the other half: an
// internal user who is not a manager must not receive customization controls
// that the backend would reject.
func TestPortalBootstrapCanManageFalseForNonManager(t *testing.T) {
	db, _, _ := setupPortalBootstrapTest(t)
	handler := NewPortalHandler(db, nil, nil, nil, blobstore.Disabled())
	handler.SetChannelService(services.NewChannelService(db, nil))

	portal := decodeBootstrapPortal(t, handler, portalBootstrapRequestWithInternalUser(1))
	if portal["can_manage"] != false {
		t.Fatalf("can_manage = %v, want false for non-manager", portal["can_manage"])
	}
}

// TestPortalBootstrapCanManageFalseForPortalCustomer ensures portal customers
// never receive the internal customization surface.
func TestPortalBootstrapCanManageFalseForPortalCustomer(t *testing.T) {
	db, _, _ := setupPortalBootstrapTest(t)
	handler := NewPortalHandler(db, nil, nil, nil, blobstore.Disabled())
	handler.SetChannelService(services.NewChannelService(db, nil))

	portal := decodeBootstrapPortal(t, handler, portalBootstrapRequestWithCustomer(7))
	if portal["can_manage"] != false {
		t.Fatalf("can_manage = %v, want false for portal customer", portal["can_manage"])
	}
}
