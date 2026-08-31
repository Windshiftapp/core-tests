package handlers

import (
	"database/sql"
	"testing"

	"windshift/internal/models"
)

func TestVisibilityJSONFailsClosedWhenMalformed(t *testing.T) {
	malformed := sql.NullString{String: `{"unexpected":true}`, Valid: true}
	if _, err := unmarshalIntIDs(malformed); err == nil {
		t.Fatal("malformed visibility JSON unexpectedly decoded as unrestricted")
	}

	requestType := &models.RequestType{ID: 7}
	if err := applyRequestTypeVisibility(requestType, malformed, sql.NullString{}); err == nil {
		t.Fatal("request type accepted malformed visibility JSON")
	}
}

func TestVisibilityJSONDecodesValidIDLists(t *testing.T) {
	groups := sql.NullString{String: `[2,4]`, Valid: true}
	orgs := sql.NullString{String: `[9]`, Valid: true}
	requestType := &models.RequestType{ID: 7}
	if err := applyRequestTypeVisibility(requestType, groups, orgs); err != nil {
		t.Fatalf("applyRequestTypeVisibility: %v", err)
	}
	if len(requestType.VisibilityGroupIDs) != 2 || requestType.VisibilityOrgIDs[0] != 9 {
		t.Fatalf("decoded visibility = groups %v orgs %v", requestType.VisibilityGroupIDs, requestType.VisibilityOrgIDs)
	}
}

func TestChannelResourceOwnershipTypes(t *testing.T) {
	portal := &models.Channel{Type: "portal", Direction: "inbound"}
	form := &models.Channel{Type: "form", Direction: "inbound"}
	webhook := &models.Channel{Type: "webhook", Direction: "outbound"}

	if !channelSupportsRequestTypes(portal) || !channelSupportsRequestTypes(form) {
		t.Fatal("inbound portal/form channels should support request types")
	}
	if channelSupportsRequestTypes(webhook) || channelSupportsRequestTypes(&models.Channel{Type: "portal", Direction: "outbound"}) {
		t.Fatal("non-public-inbound channels unexpectedly support request types")
	}
	if !channelSupportsAssetReports(portal) {
		t.Fatal("inbound portal should support asset reports")
	}
	if channelSupportsAssetReports(form) || channelSupportsAssetReports(webhook) {
		t.Fatal("non-portal channels unexpectedly support asset reports")
	}
}

func TestEffectiveRequestTypeWorkspaceUsesPublicRuntimeRouting(t *testing.T) {
	served := []int{11, 22}
	if got, ok := effectiveRequestTypeWorkspace(served, nil); !ok || got != 11 {
		t.Fatalf("legacy route = (%d, %v), want first workspace (11, true)", got, ok)
	}
	pinned := 22
	if got, ok := effectiveRequestTypeWorkspace(served, &pinned); !ok || got != 22 {
		t.Fatalf("pinned route = (%d, %v), want (22, true)", got, ok)
	}
	if _, ok := effectiveRequestTypeWorkspace(nil, nil); ok {
		t.Fatal("legacy route without served workspaces unexpectedly resolved")
	}
}
