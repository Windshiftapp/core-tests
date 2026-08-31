package auth

import (
	"testing"
	"time"
)

func TestCreateSessionRememberMeUsesExtendedDuration(t *testing.T) {
	_, manager, defaultSession := newSessionValidationTestManager(t, 0)

	before := time.Now()
	rememberedSession, err := manager.CreateSession(defaultSession.UserID, sessionValidationTestIP, "test-agent", true)
	if err != nil {
		t.Fatalf("CreateSession remember-me: %v", err)
	}
	after := time.Now()

	if rememberedSession.ExpiresAt.Before(before.Add(ExtendedSessionDuration)) ||
		rememberedSession.ExpiresAt.After(after.Add(ExtendedSessionDuration)) {
		t.Fatalf("remember-me expiry = %v, want creation time + %v", rememberedSession.ExpiresAt, ExtendedSessionDuration)
	}
	if defaultSession.ExpiresAt.After(before.Add(DefaultSessionDuration + time.Minute)) {
		t.Fatalf("default expiry = %v, want approximately %v", defaultSession.ExpiresAt, DefaultSessionDuration)
	}
}
