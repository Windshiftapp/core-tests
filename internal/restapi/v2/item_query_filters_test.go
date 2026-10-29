//go:build test

package v2

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestItemListKeepsExplicitFiltersWithCollectionAndQL(t *testing.T) {
	for _, scope := range []string{"collection_id=8", "ql=status_completed%20%3D%20false"} {
		t.Run(scope, func(t *testing.T) {
			_, request, err := parseItemList(httptest.NewRequest(http.MethodGet, "/items?"+scope+"&id=42&assignee_id=7&priority_id=2&parent_id=null", nil), 1)
			if err != nil {
				t.Fatal(err)
			}
			filters := request.Filters
			if filters.ItemID == nil || *filters.ItemID != 42 || filters.AssigneeID == nil || *filters.AssigneeID != 7 || filters.PriorityID == nil || *filters.PriorityID != 2 || !filters.ParentIDIsSet || filters.ParentID == nil || *filters.ParentID != 0 {
				t.Fatalf("explicit query filters were dropped: %+v", filters)
			}
		})
	}
}
