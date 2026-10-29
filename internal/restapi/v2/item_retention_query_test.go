//go:build test

package v2

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"
)

func TestItemListParsesCompletedActivityRetention(t *testing.T) {
	for _, scope := range []string{"workspace_id=7", "collection_id=8"} {
		t.Run(scope, func(t *testing.T) {
			_, request, err := parseItemList(httptest.NewRequest(http.MethodGet, "/items?"+scope+"&completed_activity_days=30", nil), 1)
			if err != nil {
				t.Fatal(err)
			}
			if request.Filters.CompletedActivitySince == nil {
				t.Fatal("completed_activity_days was ignored; expected an activity cutoff")
			}
		})
	}
}

func TestItemListRejectsInvalidCompletedActivityRetention(t *testing.T) {
	for _, days := range []string{"0", "-1", "3651", "abc", "1.5"} {
		t.Run(days, func(t *testing.T) {
			_, _, err := parseItemList(httptest.NewRequest(http.MethodGet, "/items?completed_activity_days="+days, nil), 1)
			var apiErr *Error
			if !errors.As(err, &apiErr) {
				t.Fatalf("expected invalid_request, got %v", err)
			}
			if apiErr.Status != http.StatusBadRequest || apiErr.Code != "invalid_request" || !reflect.DeepEqual(apiErr.Details, map[string]string{"field": "completed_activity_days"}) {
				t.Fatalf("invalid retention error = %+v", apiErr)
			}
		})
	}
}

func TestItemListCompletedActivityCutoffUsesUTCAndExactDayBounds(t *testing.T) {
	now := time.Date(2026, 9, 7, 14, 30, 0, 0, time.FixedZone("CEST", 2*60*60))
	for _, days := range []int{1, 30, 3650} {
		t.Run(fmt.Sprint(days), func(t *testing.T) {
			_, request, err := parseItemListAt(httptest.NewRequest(http.MethodGet, fmt.Sprintf("/items?completed_activity_days=%d", days), nil), 1, now)
			if err != nil {
				t.Fatal(err)
			}
			want := now.UTC().AddDate(0, 0, -days)
			if request.Filters.CompletedActivitySince == nil || *request.Filters.CompletedActivitySince != want {
				t.Fatalf("cutoff = %v, want %v", request.Filters.CompletedActivitySince, want)
			}
		})
	}
	_, request, err := parseItemListAt(httptest.NewRequest(http.MethodGet, "/items", nil), 1, now)
	if err != nil || request.Filters.CompletedActivitySince != nil {
		t.Fatalf("absent retention filter = %v, error = %v", request.Filters.CompletedActivitySince, err)
	}
}
