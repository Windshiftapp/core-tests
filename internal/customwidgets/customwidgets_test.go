//go:build test

package customwidgets

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func TestValidateProgram(t *testing.T) {
	valid := `root = Card(children=[Title(text="Hi"), ItemsTable(query="status_completed = false")])`

	tests := []struct {
		name    string
		program string
		wantErr string
	}{
		{name: "accepts a known component tree", program: valid},
		{
			name:    "accepts an unknown-looking label inside a string",
			program: `root = Text(text="Card(NotAComponent(")`,
		},
		{
			name:    "accepts a pie chart program",
			program: `root = Card(children=[PieChart(query="status_completed = false", group_by="status")])`,
		},
		{name: "rejects an empty program", program: "   ", wantErr: "program is required"},
		{
			name:    "rejects an unknown component",
			program: `root = Wibble(text="x")`,
			wantErr: `unknown component "Wibble"`,
		},
		{
			name:    "rejects a program without root",
			program: `chart = BarChart()`,
			wantErr: "root",
		},
		{
			name:    "rejects an oversized program",
			program: "root = Text(text=\"" + strings.Repeat("x", MaxProgramBytes) + "\")",
			wantErr: "exceeds",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateProgram(test.program)
			if test.wantErr == "" {
				if err != nil {
					t.Fatalf("ValidateProgram() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("ValidateProgram() = nil, want error containing %q", test.wantErr)
			}
			if !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("ValidateProgram() = %q, want error containing %q", err.Error(), test.wantErr)
			}
		})
	}
}

func TestValidateProgramStatementCap(t *testing.T) {
	var builder strings.Builder
	builder.WriteString("root = Stack([])\n")
	for i := 0; i < MaxStatements; i++ {
		builder.WriteString("node")
		builder.WriteString(strconv.Itoa(i))
		builder.WriteString(` = Text(text="x")` + "\n")
	}

	err := ValidateProgram(builder.String())
	if err == nil || !strings.Contains(err.Error(), "statements") {
		t.Fatalf("ValidateProgram() = %v, want a statement-cap error", err)
	}
}

func TestNormalizeQueries(t *testing.T) {
	t.Run("applies defaults and canonicalizes", func(t *testing.T) {
		raw := json.RawMessage(`{
			"item": {"ql": "status_completed = false"},
			"asset": {"ql": "", "asset_set_id": 7, "link_projection": {"entity": "item", "fields": ["creator_name"]}}
		}`)

		normalized, err := NormalizeQueries(raw)
		if err != nil {
			t.Fatalf("NormalizeQueries() = %v", err)
		}

		var queries Queries
		if err := json.Unmarshal(normalized, &queries); err != nil {
			t.Fatalf("unmarshal normalized: %v", err)
		}
		if queries.Item == nil || queries.Item.Limit != DefaultLimit {
			t.Fatalf("item limit = %+v, want default %d", queries.Item, DefaultLimit)
		}
		if queries.Asset == nil || queries.Asset.AssetSetID != 7 {
			t.Fatalf("asset slot = %+v, want set 7", queries.Asset)
		}
		if queries.Asset.LinkProjection == nil || queries.Asset.LinkProjection.Mode != "fanout" {
			t.Fatalf("projection = %+v, want default fanout mode", queries.Asset.LinkProjection)
		}
	})

	t.Run("empty input becomes an empty object", func(t *testing.T) {
		normalized, err := NormalizeQueries(nil)
		if err != nil {
			t.Fatalf("NormalizeQueries(nil) = %v", err)
		}
		if string(normalized) != "{}" {
			t.Fatalf("NormalizeQueries(nil) = %s, want {}", normalized)
		}
	})

	t.Run("rejects unknown fields", func(t *testing.T) {
		_, err := NormalizeQueries(json.RawMessage(`{"items": {"ql": ""}}`))
		if err == nil {
			t.Fatal("NormalizeQueries() = nil, want an unknown-field error")
		}
	})

	invalid := []struct {
		name string
		raw  string
		want string
	}{
		{name: "asset slot without a set", raw: `{"asset": {"ql": ""}}`, want: "asset_set_id"},
		{name: "item slot with a set", raw: `{"item": {"ql": "", "asset_set_id": 3}}`, want: "only valid for asset"},
		{name: "limit above the cap", raw: `{"item": {"ql": "", "limit": 5000}}`, want: "limit"},
		{name: "ql over the length cap", raw: `{"item": {"ql": "` + strings.Repeat("a", MaxQueryLength+1) + `"}}`, want: "exceeds"},
		{name: "unparseable ql", raw: `{"item": {"ql": "status = "}}`, want: "item.ql"},
		{name: "bad sort", raw: `{"item": {"ql": "", "sort": "updated at desc"}}`, want: "sort"},
		{
			name: "projection without fields",
			raw:  `{"asset": {"ql": "", "asset_set_id": 1, "link_projection": {"entity": "item"}}}`,
			want: "fields is required",
		},
		{
			name: "projection with a bad entity",
			raw:  `{"asset": {"ql": "", "asset_set_id": 1, "link_projection": {"entity": "workspace", "fields": ["title"]}}}`,
			want: "entity must be item or asset",
		},
		{
			name: "projection field cap",
			raw: `{"asset": {"ql": "", "asset_set_id": 1, "link_projection": {"entity": "item", "fields": [` +
				`"a","b","c","d","e","f","g","h","i"]}}}`,
			want: "exceeds",
		},
		{name: "links without both slots", raw: `{"links": {"item": {"ql": ""}}}`, want: "both an item and an asset slot"},
		{name: "links edge cap", raw: `{"links": {"item": {"ql": ""}, "asset": {"ql": "", "asset_set_id": 1}, "max_edges": 99999}}`, want: "max_edges"},
	}

	for _, test := range invalid {
		t.Run("rejects "+test.name, func(t *testing.T) {
			_, err := NormalizeQueries(json.RawMessage(test.raw))
			if err == nil {
				t.Fatalf("NormalizeQueries() = nil, want error containing %q", test.want)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("NormalizeQueries() = %q, want error containing %q", err.Error(), test.want)
			}
		})
	}
}

func TestNormalizeQueriesForcesLinkSeedLimits(t *testing.T) {
	raw := json.RawMessage(`{
		"links": {
			"item": {"ql": "", "limit": 5},
			"asset": {"ql": "", "asset_set_id": 2, "limit": 5}
		}
	}`)

	normalized, err := NormalizeQueries(raw)
	if err != nil {
		t.Fatalf("NormalizeQueries() = %v", err)
	}

	var queries Queries
	if err := json.Unmarshal(normalized, &queries); err != nil {
		t.Fatalf("unmarshal normalized: %v", err)
	}
	if queries.Links.Item.Limit != MaxLimit || queries.Links.Asset.Limit != MaxLimit {
		t.Fatalf("seed limits = %d/%d, want %d", queries.Links.Item.Limit, queries.Links.Asset.Limit, MaxLimit)
	}
	if queries.Links.MaxEdges != MaxEdges {
		t.Fatalf("max_edges = %d, want default %d", queries.Links.MaxEdges, MaxEdges)
	}
}
