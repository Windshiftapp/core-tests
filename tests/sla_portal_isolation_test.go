//go:build test

package tests

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"windshift/internal/services"
)

// Portal customers receive no SLA data in this initiative (WI-584 slice 11).
// These tests pin that contract so a future portal-display policy has to change
// them deliberately.

func TestPortalSessionCannotReadItemSLA(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	adminToken := CreateBearerToken(t, server)
	server.BearerToken = adminToken

	workspaceID, _ := CreateTestWorkspace(t, server, "SLA Portal Isolation", shortKey("SPI"))
	itemID := CreateTestItem(t, server, workspaceID, "Internal SLA item")
	_, channelID := SetupPortalChannel(t, server, workspaceID)
	_, sessionCookie := CreatePortalCustomerWithSession(t, server, channelID, "SLA Customer", "sla-customer@example.test")

	resp := MakePortalRequest(t, server, sessionCookie, http.MethodGet, fmt.Sprintf("/items/%d/sla", itemID), nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized && resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusForbidden {
		t.Fatalf("portal request to item SLA = %d, want the request denied (401/403/404)", resp.StatusCode)
	}

	// The internal read still works for an authenticated internal caller, which
	// proves the portal denial is an authorization boundary and not a missing
	// route.
	internal := MakeAuthRequestWithToken(t, server, adminToken, http.MethodGet, fmt.Sprintf("/items/%d/sla", itemID), nil)
	defer func() { _ = internal.Body.Close() }()
	AssertStatusCode(t, internal, http.StatusOK)
}

func TestPortalResponseTypesCarryNoSLAFields(t *testing.T) {
	t.Parallel()
	shapes := map[string]any{
		"PortalRequestSummary": services.PortalRequestSummary{},
		"PortalRequestDetail":  services.PortalRequestDetail{},
		"PortalComment":        services.PortalComment{},
	}
	for name, shape := range shapes {
		encoded, err := json.Marshal(shape)
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		if strings.Contains(strings.ToLower(string(encoded)), "\"sla") {
			t.Fatalf("%s exposes an SLA field: %s", name, encoded)
		}
	}
}
