package logbookapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakePermissionService implements the unexported bucketPermissionService
// interface so guard behavior can be tested without a real database.
type fakePermissionService struct {
	hasBucketPermission func(userID int, isAdmin bool, groupIDs []int, bucketID, permission string) (bool, error)
	accessibleBucketIDs func(userID int, isAdmin bool, groupIDs []int) ([]string, error)
}

func (f *fakePermissionService) HasBucketPermission(userID int, isAdmin bool, groupIDs []int, bucketID, permission string) (bool, error) {
	if f.hasBucketPermission != nil {
		return f.hasBucketPermission(userID, isAdmin, groupIDs, bucketID, permission)
	}
	return false, nil
}

func (f *fakePermissionService) GetAccessibleBucketIDs(userID int, isAdmin bool, groupIDs []int) ([]string, error) {
	if f.accessibleBucketIDs != nil {
		return f.accessibleBucketIDs(userID, isAdmin, groupIDs)
	}
	return nil, nil
}

func requestWithUser(user *LogbookUser, path string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if user != nil {
		ctx := context.WithValue(r.Context(), logbookUserKey{}, user)
		r = r.WithContext(ctx)
	}
	return r
}

func TestRequireBucket_NoUser(t *testing.T) {
	rec := httptest.NewRecorder()
	perms := &fakePermissionService{}
	req := requestWithUser(nil, "/api/logbook/buckets/123")
	req.SetPathValue("bucketID", "550e8400-e29b-41d4-a716-446655440000")

	_, _, ok := requireBucketView(rec, req, perms)
	if ok {
		t.Fatal("expected guard to fail without a user")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

func TestRequireBucket_MalformedBucketID(t *testing.T) {
	rec := httptest.NewRecorder()
	perms := &fakePermissionService{}
	req := requestWithUser(&LogbookUser{ID: 1}, "/api/logbook/buckets/not-a-uuid")
	req.SetPathValue("bucketID", "not-a-uuid")

	_, _, ok := requireBucketView(rec, req, perms)
	if ok {
		t.Fatal("expected guard to fail for malformed bucket ID")
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rec.Code)
	}
}

func TestRequireBucket_PermissionError(t *testing.T) {
	rec := httptest.NewRecorder()
	perms := &fakePermissionService{
		hasBucketPermission: func(int, bool, []int, string, string) (bool, error) {
			return false, errors.New("database unavailable")
		},
	}
	req := requestWithUser(&LogbookUser{ID: 1}, "/api/logbook/buckets/550e8400-e29b-41d4-a716-446655440000")
	req.SetPathValue("bucketID", "550e8400-e29b-41d4-a716-446655440000")

	_, _, ok := requireBucketView(rec, req, perms)
	if ok {
		t.Fatal("expected guard to fail on permission error")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", rec.Code)
	}
}

func TestRequireBucket_Denied(t *testing.T) {
	rec := httptest.NewRecorder()
	perms := &fakePermissionService{
		hasBucketPermission: func(int, bool, []int, string, string) (bool, error) {
			return false, nil
		},
	}
	req := requestWithUser(&LogbookUser{ID: 1}, "/api/logbook/buckets/550e8400-e29b-41d4-a716-446655440000")
	req.SetPathValue("bucketID", "550e8400-e29b-41d4-a716-446655440000")

	_, _, ok := requireBucketView(rec, req, perms)
	if ok {
		t.Fatal("expected guard to fail when permission denied")
	}
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404 (not 403) to avoid existence leak, got %d", rec.Code)
	}
}

func TestRequireBucket_AdminBypass(t *testing.T) {
	rec := httptest.NewRecorder()
	bucketID := "550e8400-e29b-41d4-a716-446655440000"
	perms := &fakePermissionService{
		hasBucketPermission: func(_ int, isAdmin bool, _ []int, id string, perm string) (bool, error) {
			if !isAdmin {
				t.Fatalf("expected admin to short-circuit permission check")
			}
			if id != bucketID {
				t.Fatalf("want bucketID %s, got %s", bucketID, id)
			}
			return true, nil
		},
	}
	req := requestWithUser(&LogbookUser{ID: 1, IsAdmin: true}, "/api/logbook/buckets/"+bucketID)
	req.SetPathValue("bucketID", bucketID)

	user, gotID, ok := requireBucketView(rec, req, perms)
	if !ok {
		t.Fatal("expected admin to pass")
	}
	if gotID != bucketID {
		t.Fatalf("want bucketID %s, got %s", bucketID, gotID)
	}
	if user == nil || user.ID != 1 {
		t.Fatalf("user not returned correctly: %+v", user)
	}
}

func TestRequireBucket_GroupPermission(t *testing.T) {
	rec := httptest.NewRecorder()
	bucketID := "550e8400-e29b-41d4-a716-446655440000"
	perms := &fakePermissionService{
		hasBucketPermission: func(_ int, _ bool, groupIDs []int, id string, _ string) (bool, error) {
			if id != bucketID {
				t.Fatalf("want bucketID %s, got %s", bucketID, id)
			}
			for _, g := range groupIDs {
				if g == 7 {
					return true, nil
				}
			}
			return false, nil
		},
	}
	req := requestWithUser(&LogbookUser{ID: 1, GroupIDs: []int{7, 8}}, "/api/logbook/buckets/"+bucketID)
	req.SetPathValue("bucketID", bucketID)

	_, gotID, ok := requireBucketEdit(rec, req, perms)
	if !ok {
		t.Fatal("expected group permission to pass")
	}
	if gotID != bucketID {
		t.Fatalf("want bucketID %s, got %s", bucketID, gotID)
	}
}

func TestRequireBucket_PermissionSpecificity(t *testing.T) {
	rec := httptest.NewRecorder()
	bucketID := "550e8400-e29b-41d4-a716-446655440000"
	seen := map[string]bool{}
	perms := &fakePermissionService{
		hasBucketPermission: func(_ int, _ bool, _ []int, id string, perm string) (bool, error) {
			if id == bucketID {
				seen[perm] = true
			}
			return true, nil
		},
	}
	req := requestWithUser(&LogbookUser{ID: 1}, "/api/logbook/buckets/"+bucketID)
	req.SetPathValue("bucketID", bucketID)

	requireBucketAdmin(rec, req, perms)
	if !seen["bucket.admin"] {
		t.Fatal("admin guard did not request bucket.admin permission")
	}
}

func TestRequireAccessibleBuckets_NoUser(t *testing.T) {
	rec := httptest.NewRecorder()
	perms := &fakePermissionService{}
	req := requestWithUser(nil, "/api/logbook/documents")

	_, ok := requireAccessibleBuckets(rec, req, perms)
	if ok {
		t.Fatal("expected accessible-bucket guard to fail without a user")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

func TestRequireAccessibleBuckets_Error(t *testing.T) {
	rec := httptest.NewRecorder()
	perms := &fakePermissionService{
		accessibleBucketIDs: func(int, bool, []int) ([]string, error) {
			return nil, errors.New("database unavailable")
		},
	}
	req := requestWithUser(&LogbookUser{ID: 1}, "/api/logbook/documents")

	_, ok := requireAccessibleBuckets(rec, req, perms)
	if ok {
		t.Fatal("expected accessible-bucket guard to fail on error")
	}
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", rec.Code)
	}
}

func TestRequireAccessibleBuckets_AdminBypass(t *testing.T) {
	rec := httptest.NewRecorder()
	perms := &fakePermissionService{
		accessibleBucketIDs: func(_ int, isAdmin bool, _ []int) ([]string, error) {
			if !isAdmin {
				t.Fatal("expected admin flag to be passed through")
			}
			return []string{"b1", "b2"}, nil
		},
	}
	req := requestWithUser(&LogbookUser{ID: 1, IsAdmin: true}, "/api/logbook/documents")

	ids, ok := requireAccessibleBuckets(rec, req, perms)
	if !ok {
		t.Fatal("expected accessible-bucket guard to pass")
	}
	if len(ids) != 2 || ids[0] != "b1" || ids[1] != "b2" {
		t.Fatalf("unexpected ids: %v", ids)
	}
}

func TestRequireSystemAdmin_NonAdmin(t *testing.T) {
	rec := httptest.NewRecorder()
	req := requestWithUser(&LogbookUser{ID: 1, IsAdmin: false}, "/api/logbook/buckets")

	_, ok := requireSystemAdmin(rec, req)
	if ok {
		t.Fatal("expected non-admin to be rejected")
	}
	if rec.Code != http.StatusForbidden {
		t.Fatalf("want 403, got %d", rec.Code)
	}
}

func TestRequireSystemAdmin_Admin(t *testing.T) {
	rec := httptest.NewRecorder()
	req := requestWithUser(&LogbookUser{ID: 1, IsAdmin: true}, "/api/logbook/buckets")

	user, ok := requireSystemAdmin(rec, req)
	if !ok {
		t.Fatal("expected admin to pass")
	}
	if user == nil || !user.IsAdmin {
		t.Fatalf("user not returned correctly: %+v", user)
	}
}

func TestRequireBucketAccessForUser_PropagatesErrorAndDenial(t *testing.T) {
	bucketID := "550e8400-e29b-41d4-a716-446655440000"
	user := &LogbookUser{ID: 1}

	t.Run("error", func(t *testing.T) {
		rec := httptest.NewRecorder()
		perms := &fakePermissionService{
			hasBucketPermission: func(int, bool, []int, string, string) (bool, error) {
				return false, errors.New("boom")
			},
		}
		if requireBucketAccessForUser(rec, requestWithUser(user, "/"), perms, user, bucketID, "bucket.view") {
			t.Fatal("expected false on permission error")
		}
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("want 500, got %d", rec.Code)
		}
	})

	t.Run("denied", func(t *testing.T) {
		rec := httptest.NewRecorder()
		perms := &fakePermissionService{
			hasBucketPermission: func(int, bool, []int, string, string) (bool, error) {
				return false, nil
			},
		}
		if requireBucketAccessForUser(rec, requestWithUser(user, "/"), perms, user, bucketID, "bucket.view") {
			t.Fatal("expected false when denied")
		}
		if rec.Code != http.StatusNotFound {
			t.Fatalf("want 404, got %d", rec.Code)
		}
	})

	t.Run("allowed", func(t *testing.T) {
		rec := httptest.NewRecorder()
		perms := &fakePermissionService{
			hasBucketPermission: func(int, bool, []int, string, string) (bool, error) {
				return true, nil
			},
		}
		if !requireBucketAccessForUser(rec, requestWithUser(user, "/"), perms, user, bucketID, "bucket.view") {
			t.Fatal("expected true when allowed")
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("expected recorder left at default 200, got %d", rec.Code)
		}
	})
}
