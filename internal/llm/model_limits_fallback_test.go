package llm

import (
	"context"
	"fmt"
	"testing"
	"time"

	"windshift/internal/database"
)

// TestApplyFallbackModelLimits covers the fill-in applied when a catalog does
// not advertise a model's limits.
func TestApplyFallbackModelLimits(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		contextIn, outputIn     int
		wantContext, wantOutput int
		wantApplied             bool
	}{
		{
			name:      "both resolved are left untouched",
			contextIn: 200000, outputIn: 8192,
			wantContext: 200000, wantOutput: 8192, wantApplied: false,
		},
		{
			name:      "nothing resolved falls back to both floors",
			contextIn: 0, outputIn: 0,
			wantContext: fallbackContextWindow, wantOutput: fallbackMaxOutputTokens, wantApplied: true,
		},
		{
			name:      "advertised context keeps its value while output falls back",
			contextIn: 32000, outputIn: 0,
			wantContext: 32000, wantOutput: fallbackMaxOutputTokens, wantApplied: true,
		},
		{
			name:      "output floor never exceeds a smaller advertised context",
			contextIn: 2048, outputIn: 0,
			wantContext: 2048, wantOutput: 2048, wantApplied: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &ConnectionRuntimeConfig{
				ProviderType: "local", Model: "m",
				ContextWindow: tc.contextIn, MaxOutputTokens: tc.outputIn,
			}
			applied := applyFallbackModelLimits(cfg)
			if cfg.ContextWindow != tc.wantContext || cfg.MaxOutputTokens != tc.wantOutput {
				t.Fatalf("limits = (%d, %d), want (%d, %d)",
					cfg.ContextWindow, cfg.MaxOutputTokens, tc.wantContext, tc.wantOutput)
			}
			if applied != tc.wantApplied {
				t.Fatalf("applied = %v, want %v", applied, tc.wantApplied)
			}
		})
	}
}

// TestConnectionRuntimeResolvesModelsWithoutCatalogLimits pins the regression
// that made WI-920 necessary: several providers advertise no limits at all
// (Anthropic's /v1/models carries none, OpenAI's carries neither a context
// length nor an output cap, and the local provider has no static model list),
// and refusing to resolve those connections took out binding validation,
// profile creation, and run start along with the inference call.
func TestConnectionRuntimeResolvesModelsWithoutCatalogLimits(t *testing.T) {
	LoadDefaultProviders()
	dsn := fmt.Sprintf("file:%s/limitfallback.db?mode=memory&cache=shared", t.TempDir())
	db, err := database.NewSQLiteDBWithPoolSizes(dsn, 4, 1)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Initialize(); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	cache := NewModelCache(db)
	// Shapes the live catalogs actually return: ids only for a local
	// OpenAI-compatible server and for Anthropic, and an OpenRouter entry whose
	// top_provider.max_completion_tokens is absent.
	if err := cache.SaveSuccess("local", []ModelInfo{{ID: "qwen3-coder:30b"}}, time.Now()); err != nil {
		t.Fatalf("seed local cache: %v", err)
	}
	if err := cache.SaveSuccess("anthropic", []ModelInfo{{ID: "claude-sonnet-4-5-20250929"}}, time.Now()); err != nil {
		t.Fatalf("seed anthropic cache: %v", err)
	}
	if err := cache.SaveSuccess("openrouter", []ModelInfo{{ID: "vendor/no-output-cap", ContextWindow: 200000}}, time.Now()); err != nil {
		t.Fatalf("seed openrouter cache: %v", err)
	}

	if _, err := db.ExecWrite(`INSERT INTO llm_connections(name, provider_type, model, is_enabled) VALUES
		('local',      'local',      'qwen3-coder:30b',           TRUE),
		('anthropic',  'anthropic',  'claude-sonnet-4-5-20250929', TRUE),
		('openrouter', 'openrouter', 'vendor/no-output-cap',      TRUE),
		('catalog',    'openai',     'gpt-4o',                    TRUE)`); err != nil {
		t.Fatalf("seed connections: %v", err)
	}

	m := NewConnectionManager(db, nil, nil)
	m.SetModelCache(cache)

	for _, tc := range []struct {
		connection              int
		name                    string
		wantContext, wantOutput int
	}{
		{connection: 1, name: "local", wantContext: fallbackContextWindow, wantOutput: fallbackMaxOutputTokens},
		{connection: 2, name: "anthropic", wantContext: fallbackContextWindow, wantOutput: fallbackMaxOutputTokens},
		// A partially advertised model keeps what the catalog did supply.
		{connection: 3, name: "openrouter", wantContext: 200000, wantOutput: fallbackMaxOutputTokens},
		// A fully catalogued model must not be touched by the fallback.
		{connection: 4, name: "catalog", wantContext: 128000, wantOutput: 16384},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := m.ConnectionRuntime(context.Background(), tc.connection)
			if err != nil {
				t.Fatalf("ConnectionRuntime() error = %v, want a resolved config", err)
			}
			if cfg.ContextWindow != tc.wantContext || cfg.MaxOutputTokens != tc.wantOutput {
				t.Fatalf("limits = (%d, %d), want (%d, %d)",
					cfg.ContextWindow, cfg.MaxOutputTokens, tc.wantContext, tc.wantOutput)
			}
		})
	}
}
