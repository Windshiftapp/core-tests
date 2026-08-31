package services

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestEnsureDefaultPortalSectionRejectsNullConfig(t *testing.T) {
	if _, err := ensureDefaultPortalSection("null"); err == nil {
		t.Fatal("null portal config unexpectedly succeeded")
	}
}

func TestEnsureDefaultPortalSectionSetsInitialGradient(t *testing.T) {
	config, err := ensureDefaultPortalSection(`{"portal_title":"Help"}`)
	if err != nil {
		t.Fatalf("ensureDefaultPortalSection() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(config), &got); err != nil {
		t.Fatalf("decode portal config: %v", err)
	}
	if got["portal_gradient"] != float64(1) {
		t.Fatalf("portal_gradient = %v, want 1", got["portal_gradient"])
	}
}

func TestEnsureDefaultPortalSectionPreservesExplicitNoGradient(t *testing.T) {
	config, err := ensureDefaultPortalSection(`{"portal_gradient":0}`)
	if err != nil {
		t.Fatalf("ensureDefaultPortalSection() error = %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal([]byte(config), &got); err != nil {
		t.Fatalf("decode portal config: %v", err)
	}
	if got["portal_gradient"] != float64(0) {
		t.Fatalf("portal_gradient = %v, want 0", got["portal_gradient"])
	}
}

func TestCreateChannelRejectsGenericDefaultCreation(t *testing.T) {
	service := &ChannelService{}
	_, err := service.Create(context.Background(), ChannelCreateRequest{
		Name:      "Replacement SMTP",
		Type:      "smtp",
		Direction: "outbound",
		IsDefault: true,
	})
	if !errors.Is(err, ErrInvalidChannelField) {
		t.Fatalf("Create(default) error = %v, want ErrInvalidChannelField", err)
	}
}
