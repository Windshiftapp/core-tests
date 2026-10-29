package auth

import (
	"errors"
	"strings"
	"testing"
)

func TestValidatePasswordLengthUsesBcryptByteLimit(t *testing.T) {
	tests := []struct {
		name     string
		password string
		wantErr  bool
	}{
		{name: "empty", password: ""},
		{name: "short", password: "short"},
		{name: "exactly 72 ascii bytes", password: strings.Repeat("a", 72)},
		{name: "73 ascii bytes", password: strings.Repeat("a", 73), wantErr: true},
		// 24 three-byte runes is exactly 72 bytes, 25 is 75 bytes. A rune-based
		// limit would wrongly accept the latter.
		{name: "72 bytes of multibyte runes", password: strings.Repeat("界", 24)},
		{name: "75 bytes of multibyte runes", password: strings.Repeat("界", 25), wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePasswordLength(tt.password)
			if tt.wantErr {
				if !errors.Is(err, ErrPasswordTooLong) {
					t.Fatalf("error = %v, want ErrPasswordTooLong", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("error = %v, want nil", err)
			}
		})
	}
}
