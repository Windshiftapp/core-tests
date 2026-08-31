package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestLoadProviders_EnrichesStaticModels verifies static seed models are run
// through the curated vision map at load time, so a providers file listing a
// known vision model without an explicit supports_vision still resolves capable.
func TestLoadProviders_EnrichesStaticModels(t *testing.T) {
	t.Cleanup(LoadDefaultProviders) // restore the embedded registry for other tests
	json := `{"providers":[{"type":"openai","name":"OpenAI","api_format":"openai","base_url":"https://api.openai.com",
		"models":[{"id":"gpt-4o","name":"GPT-4o","max_tokens":4096},{"id":"text-only-x","name":"X","max_tokens":4096}]}]}`
	if err := loadProvidersFromJSON([]byte(json)); err != nil {
		t.Fatalf("load: %v", err)
	}
	p := GetProvider("openai")
	if p == nil {
		t.Fatal("openai provider missing")
	}
	byID := indexByID(p.Models)
	if !byID["gpt-4o"].SupportsVision {
		t.Error("static gpt-4o should be enriched to vision-capable by the curated map")
	}
	if byID["text-only-x"].SupportsVision {
		t.Error("an unrecognized static model must stay vision-off")
	}
}

// TestRefresher_OpenRouterModalitiesDriveVision verifies the authoritative
// catalog signal: architecture.input_modalities containing "image" marks the
// model vision-capable, and its absence leaves a non-curated id text-only.
func TestRefresher_OpenRouterModalitiesDriveVision(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// First model: explicit image modality. Second: text-only modality on
		// an id the curated map does not recognize → stays vision-off.
		_, _ = w.Write([]byte(`{"data":[
			{"id":"vendor/multimodal-x","name":"Multimodal X","context_length":128000,"architecture":{"input_modalities":["text","image"]}},
			{"id":"vendor/text-only-y","name":"Text Only Y","context_length":64000,"architecture":{"input_modalities":["text"]}}
		]}`))
	}))
	defer srv.Close()

	db := newRefresherTestDB(t)
	cache := NewModelCache(db)
	refresher := newModelRefresherWithClient(cache, http.DefaultClient)
	provider := ProviderInfo{Type: "openrouter", BaseURL: srv.URL, ModelsEndpoint: "/models"}

	models, err := refresher.Refresh(context.Background(), provider, "", "")
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	byID := indexByID(models)
	if !byID["vendor/multimodal-x"].SupportsVision {
		t.Error("image modality should mark the model vision-capable")
	}
	if byID["vendor/text-only-y"].SupportsVision {
		t.Error("text-only modality on an unrecognized id should stay vision-off")
	}
}

// TestRefresher_CuratedMapFallback verifies that when the catalog omits
// modalities, the curated map fills SupportsVision by model id, and the
// persisted cache carries the flag.
func TestRefresher_CuratedMapFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// No architecture block at all — mirrors OpenAI/Z.AI/local catalogs.
		_, _ = w.Write([]byte(`{"data":[
			{"id":"openai/gpt-4o","name":"GPT-4o","context_length":128000},
			{"id":"deepseek/deepseek-chat-v3.1","name":"DeepSeek v3.1","context_length":64000}
		]}`))
	}))
	defer srv.Close()

	db := newRefresherTestDB(t)
	cache := NewModelCache(db)
	refresher := newModelRefresherWithClient(cache, http.DefaultClient)
	provider := ProviderInfo{Type: "openrouter", BaseURL: srv.URL, ModelsEndpoint: "/models"}

	if _, err := refresher.Refresh(context.Background(), provider, "", ""); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	got, err := cache.Get("openrouter")
	if err != nil {
		t.Fatalf("cache get: %v", err)
	}
	byID := indexByID(got.Models)
	if !byID["openai/gpt-4o"].SupportsVision {
		t.Error("curated map should mark gpt-4o vision-capable")
	}
	if byID["deepseek/deepseek-chat-v3.1"].SupportsVision {
		t.Error("deepseek chat is text-only and must not be marked vision-capable")
	}
}

// TestCacheGet_ReappliesVisionMap proves a cache row persisted before the flag
// existed (models_json without supports_vision) is enriched on read.
func TestCacheGet_ReappliesVisionMap(t *testing.T) {
	db := newRefresherTestDB(t)
	// Simulate a legacy cache write: no supports_vision key in the JSON.
	if _, err := db.ExecWrite(
		`INSERT INTO llm_provider_model_cache (provider_type, models_json, last_refreshed_at, updated_at)
		 VALUES (?, ?, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		"openrouter",
		`[{"id":"anthropic/claude-sonnet-4","name":"Claude Sonnet 4","max_tokens":8192}]`,
	); err != nil {
		t.Fatalf("seed legacy cache: %v", err)
	}
	cache := NewModelCache(db)
	got, err := cache.Get("openrouter")
	if err != nil {
		t.Fatalf("cache get: %v", err)
	}
	if len(got.Models) != 1 || !got.Models[0].SupportsVision {
		t.Errorf("read-time enrichment should mark claude-sonnet-4 vision-capable, got %+v", got.Models)
	}
}

// TestEnrichModelsVision_NoDowngrade verifies the curated pass never clears a
// flag the catalog already set, even for an id the map doesn't recognize.
func TestEnrichModelsVision_NoDowngrade(t *testing.T) {
	models := []ModelInfo{
		{ID: "vendor/unknown-but-multimodal", SupportsVision: true},
		{ID: "vendor/unknown-text-only", SupportsVision: false},
	}
	EnrichModelsVision("openrouter", models)
	if !models[0].SupportsVision {
		t.Error("must not downgrade a catalog-marked vision model")
	}
	if models[1].SupportsVision {
		t.Error("unknown text-only id should stay vision-off")
	}
}

func indexByID(models []ModelInfo) map[string]ModelInfo {
	m := make(map[string]ModelInfo, len(models))
	for _, mi := range models {
		m[mi.ID] = mi
	}
	return m
}
