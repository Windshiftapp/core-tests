package handlers

import (
	"strings"
	"testing"

	"golang.org/x/crypto/bcrypt"

	"windshift/internal/models"
)

// TestSetupPasswordLengthRejectsInputsOverBcryptLimit pins the setup validation
// boundary. x/crypto/bcrypt refuses inputs longer than 72 bytes instead of
// truncating them, so without the length check an over-long password reaches
// hashing and the request fails as a generic 500 with no usable message.
//
// White-box on purpose. The setup route cannot be exercised through HTTP in this
// suite, because the shared fixture completes setup before any test runs, and the
// route is guarded by RequireSetupNotComplete. The invariant here is the validation
// itself, which no public path exposes after setup.
func TestSetupPasswordLengthRejectsInputsOverBcryptLimit(t *testing.T) {
	h := &SetupHandler{}

	valid := models.SetupRequest{AdminUser: models.SetupUser{
		Email:     "setup-limit@example.test",
		Username:  "setup-limit",
		FirstName: "Setup",
		LastName:  "Limit",
	}}

	cases := []struct {
		name      string
		password  string
		wantError bool
	}{
		{"71 bytes passes", strings.Repeat("a", 71), false},
		{"exactly 72 bytes passes", strings.Repeat("a", 72), false},
		{"73 bytes is refused", strings.Repeat("a", 73), true},
		{"multibyte characters count as bytes", strings.Repeat("é", 37), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := valid
			req.AdminUser.Password = tc.password

			err := h.validateSetupRequest(req)
			if tc.wantError {
				if err == nil {
					t.Fatalf("%d bytes: expected an error, got none", len(tc.password))
				}
				if !strings.Contains(err.Error(), "72 bytes") {
					t.Fatalf("%d bytes: error does not state the limit: %v", len(tc.password), err)
				}
				return
			}

			if err != nil {
				t.Fatalf("%d bytes: unexpected error: %v", len(tc.password), err)
			}
		})
	}

	// The check has to run before hashing. If the validation limit is raised
	// without bcrypt's, the generic 500 comes back, so assert that bcrypt itself
	// still refuses 73 bytes.
	if _, err := bcrypt.GenerateFromPassword([]byte(strings.Repeat("a", 73)), bcrypt.DefaultCost); err == nil {
		t.Fatal("bcrypt accepted 73 bytes, so the validation message is stale")
	}
}
