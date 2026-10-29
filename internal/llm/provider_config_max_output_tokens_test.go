package llm

import (
	"context"
	"fmt"
	"testing"

	"windshift/internal/database"
)

func TestValidateProviderConfigMaxOutputTokens(t *testing.T) {
	for _, tc := range []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{name: "absent is valid", raw: `{"provider":{"sort":"latency"}}`},
		{name: "positive integer is valid", raw: `{"max_output_tokens":65536}`},
		{name: "zero is valid (use the catalog)", raw: `{"max_output_tokens":0}`},
		{name: "negative is rejected", raw: `{"max_output_tokens":-1}`, wantErr: true},
		{name: "string is rejected", raw: `{"max_output_tokens":"large"}`, wantErr: true},
		{name: "fractional is rejected", raw: `{"max_output_tokens":1.5}`, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateProviderConfig(tc.raw)
			if tc.wantErr && err == nil {
				t.Fatalf("ValidateProviderConfig(%q) accepted invalid value", tc.raw)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ValidateProviderConfig(%q) rejected valid value: %v", tc.raw, err)
			}
		})
	}
}

func TestProviderConfigMaxOutputTokens(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  string
		want int
	}{
		{name: "reads the override", raw: `{"max_output_tokens":65536}`, want: 65536},
		{name: "absent is zero", raw: `{"provider":{"sort":"latency"}}`, want: 0},
		{name: "empty is zero", raw: ``, want: 0},
		{name: "malformed is zero", raw: `{"max_output_tokens":`, want: 0},
		{name: "negative is zero", raw: `{"max_output_tokens":-1}`, want: 0},
		{name: "non-numeric is zero", raw: `{"max_output_tokens":"large"}`, want: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ProviderConfigMaxOutputTokens(tc.raw); got != tc.want {
				t.Fatalf("ProviderConfigMaxOutputTokens(%q) = %d, want %d", tc.raw, got, tc.want)
			}
		})
	}
}

// The per-connection override wins over the catalog's advertised output limit,
// but is still clamped to the context window so it cannot guarantee a 400.
func TestConnectionRuntimeAppliesMaxOutputTokensOverride(t *testing.T) {
	LoadDefaultProviders()
	dsn := fmt.Sprintf("file:%s/maxtokensoverride.db?mode=memory&cache=shared", t.TempDir())
	db, err := database.NewSQLiteDBWithPoolSizes(dsn, 4, 1)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	if _, err := db.ExecWrite(`INSERT INTO llm_connections(name, provider_type, model, provider_config, is_enabled) VALUES
		('override-high', 'openai',    'gpt-4o', '{"max_output_tokens":65536}', TRUE),
		('override-context-cap', 'openai', 'gpt-4o', '{"max_output_tokens":999999}', TRUE),
		('catalog-only',  'openai',    'gpt-4o', '', TRUE)`); err != nil {
		t.Fatalf("seed connections: %v", err)
	}

	m := NewConnectionManager(db, nil, nil)

	high, err := m.ConnectionRuntime(context.Background(), 1)
	if err != nil {
		t.Fatalf("ConnectionRuntime(override-high): %v", err)
	}
	if high.MaxOutputTokens != 65536 {
		t.Fatalf("override-high MaxOutputTokens = %d, want 65536", high.MaxOutputTokens)
	}
	if got := m.MaxOutputTokens(1); got != 65536 {
		t.Fatalf("MaxOutputTokens(1) = %d, want 65536", got)
	}

	capped, err := m.ConnectionRuntime(context.Background(), 2)
	if err != nil {
		t.Fatalf("ConnectionRuntime(override-context-cap): %v", err)
	}
	if capped.MaxOutputTokens != capped.ContextWindow {
		t.Fatalf("context cap = %d, want %d", capped.MaxOutputTokens, capped.ContextWindow)
	}

	// No override: the catalog's advertised limit (gpt-4o = 16384) is used.
	catalog, err := m.ConnectionRuntime(context.Background(), 3)
	if err != nil {
		t.Fatalf("ConnectionRuntime(catalog-only): %v", err)
	}
	if catalog.MaxOutputTokens != 16384 {
		t.Fatalf("catalog-only MaxOutputTokens = %d, want 16384", catalog.MaxOutputTokens)
	}
}
