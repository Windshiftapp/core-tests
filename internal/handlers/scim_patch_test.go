//go:build test

package handlers

import (
	"testing"

	"windshift/internal/models"
)

// TestUserPatchAccumulator_PathlessNameObject covers the Okta-style PATCH
// shape where the operation omits "path" and carries a nested name object
// (RFC 7644 §3.5.2.1/.3). Before the fix this silently no-op'd.
func TestUserPatchAccumulator_PathlessNameObject(t *testing.T) {
	snapshot := &models.User{FirstName: "Old", LastName: "Name"}
	acc := &userPatchAccumulator{}

	changes, err := acc.apply(snapshot, models.SCIMPatchOp{
		Op: "replace",
		Value: map[string]any{
			"name": map[string]any{"givenName": "Jane", "familyName": "Doe"},
		},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if acc.patch.SetFirstName == nil || *acc.patch.SetFirstName != "Jane" {
		t.Fatalf("givenName not resolved: %#v", acc.patch.SetFirstName)
	}
	if acc.patch.SetLastName == nil || *acc.patch.SetLastName != "Doe" {
		t.Fatalf("familyName not resolved: %#v", acc.patch.SetLastName)
	}
	if snapshot.FirstName != "Jane" || snapshot.LastName != "Doe" {
		t.Fatalf("snapshot not updated: %#v", snapshot)
	}
	for _, c := range changes {
		if c.NewValue == "<unsupported>" {
			t.Fatalf("nested name object must not be reported unsupported: %#v", changes)
		}
	}
}

// TestUserPatchAccumulator_PathlessObjectMixedAttributes verifies the
// path-less object form resolves each top-level key independently.
func TestUserPatchAccumulator_PathlessObjectMixedAttributes(t *testing.T) {
	snapshot := &models.User{Username: "old", IsActive: true}
	acc := &userPatchAccumulator{}

	_, err := acc.apply(snapshot, models.SCIMPatchOp{
		Op: "replace",
		Value: map[string]any{
			"userName": "new@example.com",
			"active":   false,
			"name":     map[string]any{"givenName": "New"},
		},
	})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if acc.patch.SetUsername == nil || *acc.patch.SetUsername != "new@example.com" {
		t.Fatalf("userName not resolved: %#v", acc.patch.SetUsername)
	}
	if acc.patch.SetActive == nil || *acc.patch.SetActive {
		t.Fatalf("active not resolved to false: %#v", acc.patch.SetActive)
	}
	if acc.patch.SetFirstName == nil || *acc.patch.SetFirstName != "New" {
		t.Fatalf("givenName not resolved: %#v", acc.patch.SetFirstName)
	}
	if !acc.deactivating {
		t.Fatal("true->false active transition must flag deactivation")
	}
}

// TestUserPatchAccumulator_CaseInsensitivePath covers RFC 7643 §2.1: attribute
// paths are case-insensitive.
func TestUserPatchAccumulator_CaseInsensitivePath(t *testing.T) {
	snapshot := &models.User{}
	acc := &userPatchAccumulator{}

	_, err := acc.apply(snapshot, models.SCIMPatchOp{Op: "Replace", Path: "Name.GivenName", Value: "Jane"})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if acc.patch.SetFirstName == nil || *acc.patch.SetFirstName != "Jane" {
		t.Fatalf("givenName not resolved for mixed-case path: %#v", acc.patch.SetFirstName)
	}
}

// TestUserPatchAccumulator_DisplayNameDoesNotClobberExistingFamily guards the
// derived displayName path: a single-word display name must not erase an
// existing family name.
func TestUserPatchAccumulator_DisplayNameDoesNotClobberExistingFamily(t *testing.T) {
	snapshot := &models.User{FirstName: "Cherilyn", LastName: "Doe"}
	acc := &userPatchAccumulator{}

	_, err := acc.apply(snapshot, models.SCIMPatchOp{Op: "replace", Path: "displayName", Value: "Cher"})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if acc.patch.SetFirstName == nil || *acc.patch.SetFirstName != "Cher" {
		t.Fatalf("givenName not derived: %#v", acc.patch.SetFirstName)
	}
	if acc.patch.SetLastName != nil {
		t.Fatalf("familyName must be left untouched, got %#v", *acc.patch.SetLastName)
	}
}

// TestUserPatchAccumulator_DisplayNameDerivesBoth verifies a two-part display
// name sets both components, including via name.formatted.
func TestUserPatchAccumulator_DisplayNameDerivesBoth(t *testing.T) {
	for _, path := range []string{"displayName", "name.formatted"} {
		snapshot := &models.User{}
		acc := &userPatchAccumulator{}
		_, err := acc.apply(snapshot, models.SCIMPatchOp{Op: "replace", Path: path, Value: "Ada Lovelace"})
		if err != nil {
			t.Fatalf("%s: apply: %v", path, err)
		}
		if acc.patch.SetFirstName == nil || *acc.patch.SetFirstName != "Ada" {
			t.Fatalf("%s: givenName %#v", path, acc.patch.SetFirstName)
		}
		if acc.patch.SetLastName == nil || *acc.patch.SetLastName != "Lovelace" {
			t.Fatalf("%s: familyName %#v", path, acc.patch.SetLastName)
		}
	}
}

// TestUserPatchAccumulator_RemoveNameComponents covers RFC 7644 §3.5.2.2.
func TestUserPatchAccumulator_RemoveNameComponents(t *testing.T) {
	cases := []struct {
		path         string
		wantFirstSet bool
		wantFirst    string
		wantLastSet  bool
		wantLast     string
	}{
		{"name.givenName", true, "", false, ""},
		{"name.familyName", false, "", true, ""},
		{"name", true, "", true, ""},
	}
	for _, tc := range cases {
		snapshot := &models.User{FirstName: "Jane", LastName: "Doe"}
		acc := &userPatchAccumulator{}
		if _, err := acc.apply(snapshot, models.SCIMPatchOp{Op: "remove", Path: tc.path}); err != nil {
			t.Fatalf("%s: apply: %v", tc.path, err)
		}
		if (acc.patch.SetFirstName != nil) != tc.wantFirstSet {
			t.Fatalf("%s: first set = %v want %v", tc.path, acc.patch.SetFirstName != nil, tc.wantFirstSet)
		}
		if tc.wantFirstSet && *acc.patch.SetFirstName != tc.wantFirst {
			t.Fatalf("%s: first = %q want %q", tc.path, *acc.patch.SetFirstName, tc.wantFirst)
		}
		if (acc.patch.SetLastName != nil) != tc.wantLastSet {
			t.Fatalf("%s: last set = %v want %v", tc.path, acc.patch.SetLastName != nil, tc.wantLastSet)
		}
		if tc.wantLastSet && *acc.patch.SetLastName != tc.wantLast {
			t.Fatalf("%s: last = %q want %q", tc.path, *acc.patch.SetLastName, tc.wantLast)
		}
	}
}

// TestUserPatchAccumulator_RemoveRequiresPath verifies the RFC 7644 §3.5.2.2
// contract that a remove without a path is rejected rather than ignored.
func TestUserPatchAccumulator_RemoveRequiresPath(t *testing.T) {
	acc := &userPatchAccumulator{}
	if _, err := acc.apply(&models.User{}, models.SCIMPatchOp{Op: "remove", Value: "x"}); err == nil {
		t.Fatal("remove without path must error")
	}
}

// TestUserPatchAccumulator_ActiveStrictBoolean rejects non-boolean active
// values instead of silently coercing them to false.
func TestUserPatchAccumulator_ActiveStrictBoolean(t *testing.T) {
	for _, value := range []any{"banana", 1, nil, map[string]any{}} {
		acc := &userPatchAccumulator{}
		if _, err := acc.apply(&models.User{IsActive: true}, models.SCIMPatchOp{Op: "replace", Path: "active", Value: value}); err == nil {
			t.Fatalf("active value %#v must be rejected", value)
		}
	}
}

// TestUserPatchAccumulator_Emails covers the email PATCH shapes IdPs use:
// the whole array (primary wins), the scalar sub-attribute, the filter
// selector, and the path-less object form.
func TestUserPatchAccumulator_Emails(t *testing.T) {
	cases := []struct {
		name string
		op   models.SCIMPatchOp
		want string
	}{
		{
			name: "array primary wins",
			op: models.SCIMPatchOp{Op: "replace", Path: "emails", Value: []any{
				map[string]any{"value": "secondary@example.com"},
				map[string]any{"value": "primary@example.com", "primary": true},
			}},
			want: "primary@example.com",
		},
		{
			name: "array first fallback",
			op: models.SCIMPatchOp{Op: "replace", Path: "emails", Value: []any{
				map[string]any{"value": "only@example.com"},
			}},
			want: "only@example.com",
		},
		{
			name: "scalar sub-attribute",
			op:   models.SCIMPatchOp{Op: "replace", Path: "emails.value", Value: "scalar@example.com"},
			want: "scalar@example.com",
		},
		{
			name: "filter selector",
			op:   models.SCIMPatchOp{Op: "replace", Path: `emails[type eq "work"].value`, Value: "work@example.com"},
			want: "work@example.com",
		},
		{
			name: "path-less object",
			op: models.SCIMPatchOp{Op: "replace", Value: map[string]any{
				"emails": []any{map[string]any{"value": "nested@example.com", "primary": true}},
			}},
			want: "nested@example.com",
		},
	}
	for _, tc := range cases {
		snapshot := &models.User{Email: "old@example.com"}
		acc := &userPatchAccumulator{}
		if _, err := acc.apply(snapshot, tc.op); err != nil {
			t.Fatalf("%s: apply: %v", tc.name, err)
		}
		if acc.patch.SetEmail == nil || *acc.patch.SetEmail != tc.want {
			t.Fatalf("%s: SetEmail = %#v want %q", tc.name, acc.patch.SetEmail, tc.want)
		}
	}
}

// TestUserPatchAccumulator_UnsupportedAttributeIsAuditedNoop documents the
// deliberate leniency for attributes Windshift does not store.
func TestUserPatchAccumulator_UnsupportedAttributeIsAuditedNoop(t *testing.T) {
	acc := &userPatchAccumulator{}
	changes, err := acc.apply(&models.User{}, models.SCIMPatchOp{Op: "replace", Path: "nickName", Value: "jd"})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(changes) != 1 || changes[0].NewValue != "<unsupported>" {
		t.Fatalf("expected unsupported breadcrumb, got %#v", changes)
	}
}
