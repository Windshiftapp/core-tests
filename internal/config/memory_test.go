package config

import "testing"

func TestResolveMemoryBudget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		limitMB     int
		wantProcess int
		wantCache   int
		wantErr     bool
	}{
		{name: "zero selects default", limitMB: 0, wantProcess: 2048, wantCache: 512},
		{name: "minimum supported budget", limitMB: 512, wantProcess: 512, wantCache: 128},
		{name: "large process keeps bounded cache", limitMB: 8192, wantProcess: 8192, wantCache: 512},
		{name: "undersized process is rejected", limitMB: 511, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			budget, err := ResolveMemoryBudget(tt.limitMB)
			if tt.wantErr {
				if err == nil {
					t.Fatal("ResolveMemoryBudget() error = nil, want validation error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveMemoryBudget() error = %v", err)
			}
			if budget.ProcessLimitMB != tt.wantProcess || budget.CacheLimitMB != tt.wantCache {
				t.Fatalf("budget = %+v, want process=%d cache=%d", budget, tt.wantProcess, tt.wantCache)
			}
			cacheTotal := budget.ItemCacheMB + budget.PermissionCacheMB + budget.NotificationCacheMB + budget.ActivityCacheMB + budget.APITokenCacheMB + budget.SessionCacheMB + budget.SCIMTokenCacheMB
			if cacheTotal != budget.CacheLimitMB {
				t.Fatalf("per-cache total = %d MiB, aggregate = %d MiB", cacheTotal, budget.CacheLimitMB)
			}
			wantGoBytes := int64(tt.wantProcess) * 1024 * 1024 * 4 / 5
			if budget.GoLimitBytes != wantGoBytes {
				t.Fatalf("GoLimitBytes = %d, want %d", budget.GoLimitBytes, wantGoBytes)
			}
		})
	}
}

func TestSplitSSHCacheBudgetPreservesAggregate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		totalMB     int
		sshEnabled  bool
		wantPrimary int
		wantSSH     int
	}{
		{name: "SSH disabled keeps primary allocation", totalMB: 8, wantPrimary: 8},
		{name: "even allocation splits equally", totalMB: 8, sshEnabled: true, wantPrimary: 4, wantSSH: 4},
		{name: "odd allocation remainder stays primary", totalMB: 9, sshEnabled: true, wantPrimary: 5, wantSSH: 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			primary, ssh := SplitSSHCacheBudget(tt.totalMB, tt.sshEnabled)
			if primary != tt.wantPrimary || ssh != tt.wantSSH {
				t.Fatalf("SplitSSHCacheBudget(%d, %v) = (%d, %d), want (%d, %d)", tt.totalMB, tt.sshEnabled, primary, ssh, tt.wantPrimary, tt.wantSSH)
			}
			if primary+ssh != tt.totalMB {
				t.Fatalf("split total = %d MiB, want %d MiB", primary+ssh, tt.totalMB)
			}
		})
	}
}
