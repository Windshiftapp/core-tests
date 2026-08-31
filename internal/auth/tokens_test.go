package auth_test

import (
	"testing"

	"windshift/internal/auth"
	"windshift/internal/models"
)

func TestCheckTokenPermissions(t *testing.T) {
	tm := &auth.TokenManager{}

	tests := []struct {
		name        string
		permissions string
		required    []string
		want        bool
	}{
		{"exact granular match", `["items:write"]`, []string{"items:write"}, true},
		{"missing scope rejected", `["items:read"]`, []string{"items:write"}, false},
		{"write satisfies read for same resource", `["items:write"]`, []string{"items:read"}, true},
		{"read does not satisfy write", `["items:read"]`, []string{"items:write"}, false},
		{"multi-scope all required present", `["items:read","workspaces:read"]`, []string{"items:read", "workspaces:read"}, true},
		{"multi-scope one missing fails", `["items:read"]`, []string{"items:read", "workspaces:read"}, false},
		{"admin write satisfies admin read", `["admin:users:write"]`, []string{"admin:users:read"}, true},
		// Legacy "read"/"write"/"admin" are no longer expanded (WI-959): the
		// 20260808_api_token_legacy_scopes migration rewrites surviving rows to
		// granular sets, and anything still carrying a legacy string grants
		// nothing rather than silently granting a whole tier.
		{"legacy read grants nothing", `["read"]`, []string{"items:read"}, false},
		{"legacy write grants nothing", `["write"]`, []string{"items:write"}, false},
		{"legacy admin grants nothing", `["admin"]`, []string{"admin:users:write"}, false},
		{"legacy write does not cover mcp:access", `["write"]`, []string{"mcp:access"}, false},
		{"malformed JSON returns false", `not-json`, []string{"items:read"}, false},
		{"empty required returns true", `["items:read"]`, []string{}, true},
		{"empty token scopes rejects any required", `[]`, []string{"items:read"}, false},
		{"mcp:access granular match", `["mcp:access"]`, []string{"mcp:access"}, true},
		{"items:write does not satisfy mcp:access", `["items:write"]`, []string{"mcp:access"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := &models.APIToken{Permissions: tt.permissions}
			got := tm.CheckTokenPermissions(token, tt.required)
			if got != tt.want {
				t.Errorf("CheckTokenPermissions(scopes=%q, required=%v) = %v, want %v",
					tt.permissions, tt.required, got, tt.want)
			}
		})
	}
}
