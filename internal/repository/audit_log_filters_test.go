//go:build test

package repository

import (
	"net/url"
	"testing"
	"time"
)

// TestParseAuditLogFilters covers the canonical audit-log query contract
// shared by the legacy, v1, and v2 read surfaces (WI-1901).
func TestParseAuditLogFilters(t *testing.T) {
	from := "2026-01-02T03:04:05Z"
	to := "2026-01-03T03:04:05Z"
	filters, err := ParseAuditLogFilters(url.Values{
		"action_type":   {"user.create"},
		"resource_type": {"user"},
		"source":        {"SCIM"},
		"search":        {"alice"},
		"user_id":       {"7"},
		"success":       {"false"},
		"from":          {from},
		"to":            {to},
	})
	if err != nil {
		t.Fatalf("ParseAuditLogFilters: %v", err)
	}
	if filters.ActionType != "user.create" || filters.ResourceType != "user" || filters.Source != "SCIM" || filters.Search != "alice" {
		t.Fatalf("string filters = %#v", filters)
	}
	if filters.UserID == nil || *filters.UserID != 7 {
		t.Fatalf("user_id = %#v", filters.UserID)
	}
	if filters.Success == nil || *filters.Success {
		t.Fatalf("success = %#v", filters.Success)
	}
	wantFrom, _ := time.Parse(time.RFC3339, from)
	wantTo, _ := time.Parse(time.RFC3339, to)
	if filters.From == nil || !filters.From.Equal(wantFrom) || filters.To == nil || !filters.To.Equal(wantTo) {
		t.Fatalf("time bounds = %#v .. %#v", filters.From, filters.To)
	}

	details := filters.Details()
	if details["action_type"] != "user.create" || details["user_id"] != 7 || details["success"] != false {
		t.Fatalf("details = %#v", details)
	}
	if details["from"] != from || details["to"] != to {
		t.Fatalf("time details = %#v", details)
	}
}

// TestParseAuditLogFiltersEmpty verifies that an empty query yields no filters.
func TestParseAuditLogFiltersEmpty(t *testing.T) {
	filters, err := ParseAuditLogFilters(url.Values{})
	if err != nil {
		t.Fatalf("ParseAuditLogFilters: %v", err)
	}
	if len(filters.Details()) != 0 {
		t.Fatalf("empty query produced details: %#v", filters.Details())
	}
}

// TestParseAuditLogFiltersRejectsMalformed verifies the shared contract
// rejects malformed values instead of silently ignoring them, which is the
// divergence WI-1901 removed.
func TestParseAuditLogFiltersRejectsMalformed(t *testing.T) {
	tests := []struct {
		name   string
		values url.Values
	}{
		{"success", url.Values{"success": {"maybe"}}},
		{"user_id non-numeric", url.Values{"user_id": {"abc"}}},
		{"user_id zero", url.Values{"user_id": {"0"}}},
		{"user_id negative", url.Values{"user_id": {"-3"}}},
		{"from", url.Values{"from": {"yesterday"}}},
		{"to", url.Values{"to": {"2026-13-01T00:00:00Z"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseAuditLogFilters(tt.values); err == nil {
				t.Fatalf("expected a client error for %v", tt.values)
			} else if _, ok := err.(*AuditLogFilterError); !ok {
				t.Fatalf("error type = %T, want *AuditLogFilterError", err)
			}
		})
	}
}
