//go:build test

package v2

import (
	"net/http"
	"testing"
)

// TestMutatingRoutesHaveAuditSpec is the WI-1898 coverage guardrail. Every
// mutating route must be classified as AuditRecorder (the handler records via
// logger.Record), AuditService (the application service emits the event), or
// AuditExempt (a reviewed exemption). A new mutating route without a registry
// entry keeps AuditUnspecified and fails here, so it cannot ship unaudited by
// accident the way the v2 admin and attachment surfaces once did.
func TestMutatingRoutesHaveAuditSpec(t *testing.T) {
	for _, route := range Inventory() {
		if isSafeMethod(route.Method) {
			continue
		}
		switch route.Audit {
		case AuditRecorder, AuditService, AuditExempt:
		default:
			t.Errorf("%s %s (%s) has no audit spec; classify it in auditSpecRegistry",
				route.Method, route.Path, route.OperationID)
		}
	}
}

// TestAuditRegistryHasNoStaleEntries keeps the registry honest: an entry for a
// route that no longer exists hides a real missing classification behind a
// rename.
func TestAuditRegistryHasNoStaleEntries(t *testing.T) {
	live := make(map[string]struct{})
	for _, route := range Inventory() {
		if isSafeMethod(route.Method) {
			continue
		}
		live[route.OperationID] = struct{}{}
	}
	for operationID := range auditSpecRegistry {
		if _, ok := live[operationID]; !ok {
			t.Errorf("stale audit registry entry %q does not match a mutating route", operationID)
		}
	}
}

// TestAuditRegistryClassifiesRecorderSurfaces pins the surfaces that must use
// the request recorder rather than an ad-hoc handler write.
func TestAuditRegistryClassifiesRecorderSurfaces(t *testing.T) {
	want := map[string]AuditKind{
		"postAdminGroups":                         AuditRecorder,
		"patchAdminGroupsGroupId":                 AuditRecorder,
		"deleteAdminGroupsGroupId":                AuditRecorder,
		"patchAdminUsersUserId":                   AuditRecorder,
		"deleteAttachmentsAttachmentId":           AuditRecorder,
		"postItemsItemIdParticipants":             AuditRecorder,
		"deleteItemsItemIdParticipantsCustomerId": AuditRecorder,
		"deleteAdminApiTokensTokenId":             AuditRecorder,
	}
	for operationID, kind := range want {
		if auditSpecRegistry[operationID] != kind {
			t.Errorf("%s audit kind = %q, want %q", operationID, auditSpecRegistry[operationID], kind)
		}
	}
}

// TestInventoryIncludesAuditKind ensures the public inventory exposes the
// classification used by the guardrails.
func TestInventoryIncludesAuditKind(t *testing.T) {
	found := false
	for _, route := range Inventory() {
		if route.Method == http.MethodDelete && route.Audit == AuditRecorder {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected at least one DELETE route classified as AuditRecorder")
	}
}
