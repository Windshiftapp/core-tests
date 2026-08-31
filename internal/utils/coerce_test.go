//go:build test

package utils

import "testing"

func TestCoerceInt(t *testing.T) {
	tests := []struct {
		name  string
		value interface{}
		want  int
		ok    bool
	}{
		{"json number", float64(42), 42, true},
		{"fractional json number truncates", float64(3.9), 3, true},
		{"go int", int(7), 7, true},
		{"wide int", int64(9000000000), 9000000000, true},
		{"small int", int32(-5), -5, true},
		{"float32", float32(2.5), 2, true},
		{"rejects string", "42", 0, false},
		{"rejects nil", nil, 0, false},
		{"rejects bool", true, 0, false},
		{"rejects struct", struct{}{}, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := CoerceInt(tt.value)
			if ok != tt.ok {
				t.Fatalf("CoerceInt(%#v) ok = %v, want %v", tt.value, ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Fatalf("CoerceInt(%#v) = %d, want %d", tt.value, got, tt.want)
			}
		})
	}
}

func TestCoerceIntSlice(t *testing.T) {
	tests := []struct {
		name  string
		value interface{}
		want  []int
		ok    bool
	}{
		{"nil means empty", nil, []int{}, true},
		{"json numbers", []float64{1.5, 2}, []int{1, 2}, true},
		{"go ints", []int{3, 4}, []int{3, 4}, true},
		{"wide ints", []int64{5, 6}, []int{5, 6}, true},
		{"mixed interface elements", []interface{}{float64(7), int(8)}, []int{7, 8}, true},
		{"rejects non-numeric element", []interface{}{"9"}, nil, false},
		{"rejects array with zero elements as valid", []float64{}, []int{}, true},
		{"rejects scalar", 5, nil, false},
		{"rejects string", "1,2", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := CoerceIntSlice(tt.value)
			if ok != tt.ok {
				t.Fatalf("CoerceIntSlice(%#v) ok = %v, want %v", tt.value, ok, tt.ok)
			}
			if !ok {
				return
			}
			if len(got) != len(tt.want) {
				t.Fatalf("CoerceIntSlice(%#v) = %v, want %v", tt.value, got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("CoerceIntSlice(%#v) = %v, want %v", tt.value, got, tt.want)
				}
			}
		})
	}
}
