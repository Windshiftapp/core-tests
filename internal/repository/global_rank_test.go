//go:build test

package repository

import (
	"sort"
	"testing"
)

func TestGlobalRankCodecRoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		bucket   GlobalRankBucket
		fraction string
		want     string
	}{
		{name: "initial zero key", bucket: GlobalRankBucket0, fraction: "a0", want: "0|a0"},
		{name: "regular key", bucket: GlobalRankBucket1, fraction: "a1z", want: "1|a1z"},
		{name: "upper bucket", bucket: GlobalRankBucket2, fraction: "Z1", want: "2|Z1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := EncodeGlobalRank(tt.bucket, tt.fraction)
			if err != nil {
				t.Fatalf("EncodeGlobalRank() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("EncodeGlobalRank() = %q, want %q", got, tt.want)
			}

			parsed, err := ParseGlobalRank(got)
			if err != nil {
				t.Fatalf("ParseGlobalRank() error = %v", err)
			}
			if parsed.Bucket != tt.bucket || parsed.Fraction != tt.fraction {
				t.Fatalf("ParseGlobalRank() = %+v, want bucket=%d fraction=%q", parsed, tt.bucket, tt.fraction)
			}
		})
	}
}

func TestGlobalRankCodecRejectsMalformedValues(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "legacy unprefixed key", value: "a1"},
		{name: "missing fraction", value: "0|"},
		{name: "unknown bucket", value: "3|a1"},
		{name: "multi character bucket", value: "00|a1"},
		{name: "fraction separator", value: "0|a1|b1"},
		{name: "invalid fractional tail", value: "0|a10"},
		{name: "invalid fractional integer", value: "0|a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ParseGlobalRank(tt.value); err == nil {
				t.Fatalf("ParseGlobalRank(%q) succeeded, want error", tt.value)
			}
		})
	}

	if _, err := EncodeGlobalRank(GlobalRankBucket(3), "a1"); err == nil {
		t.Fatal("EncodeGlobalRank() accepted an invalid bucket")
	}
	if _, err := EncodeGlobalRank(GlobalRankBucket0, "a1|b1"); err == nil {
		t.Fatal("EncodeGlobalRank() accepted a separator in the fraction")
	}
}

func TestGlobalRankBucketTransition(t *testing.T) {
	tests := []struct {
		active        GlobalRankBucket
		wantTarget    GlobalRankBucket
		wantDirection string
	}{
		{active: GlobalRankBucket0, wantTarget: GlobalRankBucket1, wantDirection: "high_to_low"},
		{active: GlobalRankBucket1, wantTarget: GlobalRankBucket2, wantDirection: "high_to_low"},
		{active: GlobalRankBucket2, wantTarget: GlobalRankBucket0, wantDirection: "low_to_high"},
	}

	for _, tt := range tests {
		target, direction, err := GlobalRankBucketTransition(tt.active)
		if err != nil {
			t.Fatalf("GlobalRankBucketTransition(%d) error = %v", tt.active, err)
		}
		if target != tt.wantTarget || direction != tt.wantDirection {
			t.Fatalf("GlobalRankBucketTransition(%d) = (%d, %q), want (%d, %q)", tt.active, target, direction, tt.wantTarget, tt.wantDirection)
		}
	}
}

func TestGlobalRankOrderingUsesBucketPrefix(t *testing.T) {
	ranks := []string{"2|a1", "0|a2", "1|a1", "0|a1", "2|Z1"}
	sort.Strings(ranks)
	want := []string{"0|a1", "0|a2", "1|a1", "2|Z1", "2|a1"}
	for i := range want {
		if ranks[i] != want[i] {
			t.Fatalf("sorted ranks = %v, want %v", ranks, want)
		}
	}

	moved, err := WithGlobalRankBucket("0|a2", GlobalRankBucket1)
	if err != nil {
		t.Fatalf("WithGlobalRankBucket() error = %v", err)
	}
	if moved != "1|a2" {
		t.Fatalf("WithGlobalRankBucket() = %q, want %q", moved, "1|a2")
	}
}
