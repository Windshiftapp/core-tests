package logbookapi

import "testing"

func TestIsValidUUID(t *testing.T) {
	tests := []struct {
		name string
		id   string
		want bool
	}{
		{name: "canonical", id: "550e8400-e29b-41d4-a716-446655440000", want: true},
		{name: "raw hex", id: "550e8400e29b41d4a716446655440000", want: true},
		{name: "braces", id: "{550e8400-e29b-41d4-a716-446655440000}", want: true},
		{name: "lowercase URN", id: "urn:uuid:550e8400-e29b-41d4-a716-446655440000", want: true},
		{name: "uppercase URN", id: "URN:UUID:550e8400-e29b-41d4-a716-446655440000", want: false},
		{name: "arbitrary wrapper", id: "[550e8400-e29b-41d4-a716-446655440000]", want: false},
		{name: "malformed", id: "not-a-uuid", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isValidUUID(tt.id); got != tt.want {
				t.Fatalf("isValidUUID(%q) = %v, want %v", tt.id, got, tt.want)
			}
		})
	}
}
