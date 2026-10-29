package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"windshift/internal/contextkeys"
	"windshift/internal/models"
)

type stubChannelManagementCapability struct {
	manages bool
	err     error
	userID  int
}

func (s *stubChannelManagementCapability) ManagesChannels(_ context.Context, userID int) (bool, error) {
	s.userID = userID
	return s.manages, s.err
}

type stubAssetSetCapability struct {
	hasSets   bool
	manages   bool
	err       error
	manageErr error
	userID    int
}

func (s *stubAssetSetCapability) HasAccessibleAssetSets(userID int) (bool, error) {
	s.userID = userID
	return s.hasSets, s.err
}

func (s *stubAssetSetCapability) ManagesAssetSets(int) (bool, error) {
	return s.manages, s.manageErr
}

func TestShellBootstrapRequiresAuthentication(t *testing.T) {
	handler := NewShellBootstrapHandler(nil, nil, nil, nil, nil, nil, nil, nil)
	recorder := httptest.NewRecorder()

	handler.Get(recorder, httptest.NewRequest(http.MethodGet, "/api/shell-bootstrap", nil))

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestShellBootstrapComposesFeatureSnapshot(t *testing.T) {
	channels := &stubChannelManagementCapability{manages: true}
	handler := NewShellBootstrapHandler(
		NewFeaturesHandler(nil, true, true),
		nil,
		nil,
		nil,
		nil,
		nil,
		channels,
		nil,
	)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/shell-bootstrap", nil)
	request = request.WithContext(context.WithValue(request.Context(), contextkeys.User, &models.User{ID: 7}))

	handler.Get(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var response ShellBootstrapResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.Features.SSHAvailable || !response.Features.LogbookAvailable {
		t.Fatalf("feature snapshot = %+v", response.Features)
	}
	if response.AttachmentStatus == nil || response.AttachmentStatus.Enabled {
		t.Fatalf("attachment status = %+v", response.AttachmentStatus)
	}
	if !response.ManagesChannels {
		t.Fatal("manages_channels = false, want true")
	}
	if channels.userID != 7 {
		t.Fatalf("channel capability user ID = %d, want 7", channels.userID)
	}
	if response.WorkItemStaleness.StaleAfterDays != 30 {
		t.Fatalf("default stale_after_days = %d, want 30", response.WorkItemStaleness.StaleAfterDays)
	}
}

func TestShellBootstrapReportsAssetSetManagement(t *testing.T) {
	assets := &stubAssetSetCapability{hasSets: true, manages: true}
	handler := NewShellBootstrapHandler(nil, nil, nil, nil, assets, nil, nil, nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/shell-bootstrap", nil)
	request = request.WithContext(context.WithValue(request.Context(), contextkeys.User, &models.User{ID: 11}))

	handler.Get(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var response ShellBootstrapResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.HasAssetSets || !response.ManagesAssetSets {
		t.Fatalf("asset flags = has:%v manages:%v, want both true", response.HasAssetSets, response.ManagesAssetSets)
	}
	if assets.userID != 11 {
		t.Fatalf("asset capability user ID = %d, want 11", assets.userID)
	}
}

func TestShellBootstrapKeepsAssetManagementFalseWhenProbeFails(t *testing.T) {
	assets := &stubAssetSetCapability{hasSets: true, manages: true, manageErr: errors.New("database unavailable")}
	handler := NewShellBootstrapHandler(nil, nil, nil, nil, assets, nil, nil, nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/shell-bootstrap", nil)
	request = request.WithContext(context.WithValue(request.Context(), contextkeys.User, &models.User{ID: 12}))

	handler.Get(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var response ShellBootstrapResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.HasAssetSets {
		t.Fatal("has_asset_sets = false, want true")
	}
	if response.ManagesAssetSets {
		t.Fatal("manages_asset_sets = true after failed probe, want false")
	}
}

func TestShellBootstrapKeepsChannelCapabilityFalseWhenProbeFails(t *testing.T) {
	handler := NewShellBootstrapHandler(
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		&stubChannelManagementCapability{manages: true, err: errors.New("database unavailable")},
		nil,
	)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/shell-bootstrap", nil)
	request = request.WithContext(context.WithValue(request.Context(), contextkeys.User, &models.User{ID: 9}))

	handler.Get(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	var response ShellBootstrapResponse
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ManagesChannels {
		t.Fatal("manages_channels = true after failed probe, want false")
	}
}
