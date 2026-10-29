//go:build test

package handlers

import (
	"net/http/httptest"
	"testing"
)

func TestSSORememberMeFromRequest(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{
			name: "frontend remember_me parameter",
			url:  "/api/sso/login/test?remember_me=true",
			want: true,
		},
		{
			name: "legacy remember parameter",
			url:  "/api/sso/login/test?remember=true",
			want: true,
		},
		{
			name: "absent parameter",
			url:  "/api/sso/login/test",
			want: false,
		},
		{
			name: "false parameter",
			url:  "/api/sso/login/test?remember_me=false&remember=false",
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", tt.url, nil)
			if got := ssoRememberMeFromRequest(req); got != tt.want {
				t.Fatalf("ssoRememberMeFromRequest() = %v, want %v", got, tt.want)
			}
		})
	}
}
