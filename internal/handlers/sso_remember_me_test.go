package handlers

import (
	"net/http/httptest"
	"testing"
)

func TestSSORememberMeConfiguration(t *testing.T) {
	request := httptest.NewRequest("GET", "/api/sso/acme/login?remember_me=true", nil)
	if !ssoRememberMeFromRequest(request) {
		t.Fatal("remember_me=true was not recognized")
	}
}
