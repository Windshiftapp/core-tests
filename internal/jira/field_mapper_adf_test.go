package jira

import (
	"encoding/json"
	"strings"
	"testing"
)

func adfFromJSON(t *testing.T, src string) map[string]interface{} {
	t.Helper()
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(src), &out); err != nil {
		t.Fatalf("decode ADF fixture: %v", err)
	}
	return out
}

func TestConvertADFComplexBlocksToMarkdown(t *testing.T) {
	adf := adfFromJSON(t, `{
		"type":"doc",
		"version":1,
		"content":[
			{"type":"table","content":[
				{"type":"tableRow","content":[
					{"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"Name"}]}]},
					{"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"Notes"}]}]}
				]},
				{"type":"tableRow","content":[
					{"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"API"}]}]},
					{"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"uses | pipes"}]}]}
				]}
			]},
			{"type":"panel","attrs":{"panelType":"warning"},"content":[{"type":"paragraph","content":[{"type":"text","text":"Check this"}]}]},
			{"type":"taskList","content":[
				{"type":"taskItem","attrs":{"state":"DONE"},"content":[{"type":"text","text":"Ship it"}]},
				{"type":"taskItem","attrs":{"state":"TODO"},"content":[{"type":"text","text":"Document it"}]}
			]},
			{"type":"expand","attrs":{"title":"Diagnostics"},"content":[
				{"type":"paragraph","content":[{"type":"text","text":"Stack details"}]}
			]}
		]
	}`)

	got := ConvertADFToMarkdownWithUsers(adf, nil)
	for _, want := range []string{
		"| Name | Notes |",
		"| --- | --- |",
		"| API | uses \\| pipes |",
		"> [!WARNING]",
		"> Check this",
		"- [x] Ship it",
		"- [ ] Document it",
		"<details>",
		"<summary>Diagnostics</summary>",
		"Stack details",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("converted markdown missing %q\nGot:\n%s", want, got)
		}
	}
}

func TestConvertADFInlineRichNodesToMarkdown(t *testing.T) {
	adf := adfFromJSON(t, `{
		"type":"doc",
		"version":1,
		"content":[{"type":"paragraph","content":[
			{"type":"inlineCard","attrs":{"url":"https://example.test/card"}},
			{"type":"text","text":" "},
			{"type":"status","attrs":{"text":"Blocked","color":"red"}},
			{"type":"text","text":" "},
			{"type":"emoji","attrs":{"shortName":":rocket:","text":"🚀"}},
			{"type":"text","text":" "},
			{"type":"date","attrs":{"timestamp":"1735689600000"}},
			{"type":"text","text":" "},
			{"type":"mention","attrs":{"id":"acct-1","text":"@Display Name"}}
		]}]
	}`)

	got := ConvertADFToMarkdownWithUsers(adf, func(accountID string) string {
		if accountID == "acct-1" {
			return "display.name"
		}
		return ""
	})

	want := "[https://example.test/card](https://example.test/card) [Blocked] 🚀 2025-01-01 @display.name"
	if !strings.Contains(got, want) {
		t.Fatalf("converted markdown missing %q\nGot:\n%s", want, got)
	}
}

func TestConvertADFMediaLinksToImportedAttachment(t *testing.T) {
	adf := adfFromJSON(t, `{
		"type":"doc",
		"version":1,
		"content":[
			{"type":"mediaSingle","content":[{"type":"media","attrs":{"id":"att-100","type":"file","collection":"","alt":"screenshot","width":50,"height":50}}]},
			{"type":"paragraph","content":[{"type":"media","attrs":{"id":"att-101","type":"file"}}]}
		]
	}`)

	mediaResolver := NewMediaResolver(map[string]MediaAttachment{
		"att-100": {ID: 42, MimeType: "image/png", OriginalFilename: "screenshot.png"},
		"att-101": {ID: 43, MimeType: "application/pdf", OriginalFilename: "report.pdf"},
	})

	got := ConvertADFToMarkdown(adf, nil, mediaResolver)
	for _, want := range []string{
		"![screenshot.png](/api/attachments/42/download)",
		"[report.pdf](/api/attachments/43/download)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("converted markdown missing %q\nGot:\n%s", want, got)
		}
	}

	// Without a resolver (or for an unknown id) the lossy placeholder survives.
	placeholder := ConvertADFToMarkdown(adf, nil, nil)
	if !strings.Contains(placeholder, "[media:") {
		t.Fatalf("expected placeholder for unresolved media, got:\n%s", placeholder)
	}

	partial := NewMediaResolver(map[string]MediaAttachment{
		"att-100": {ID: 42, MimeType: "image/png", OriginalFilename: "screenshot.png"},
	})
	partialGot := ConvertADFToMarkdown(adf, nil, partial)
	if !strings.Contains(partialGot, "![screenshot.png](/api/attachments/42/download)") {
		t.Fatalf("expected resolved image, got:\n%s", partialGot)
	}
	if !strings.Contains(partialGot, "[media:") {
		t.Fatalf("expected placeholder for unresolved id, got:\n%s", partialGot)
	}
}

func TestCollectADFMediaIDs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		adf  any
		want map[string]bool
	}{
		{
			name: "nested media nodes are collected",
			adf: adfFromJSON(t, `{
				"type":"doc",
				"content":[
					{"type":"paragraph","content":[
						{"type":"mediaSingle","content":[
							{"type":"media","attrs":{"id":"10001"}}
						]}
					]},
					{"type":"mediaGroup","content":[
						{"type":"media","attrs":{"id":"10002"}},
						{"type":"media","attrs":{"id":"10003"}}
					]}
				]
			}`),
			want: map[string]bool{"10001": true, "10002": true, "10003": true},
		},
		{
			name: "plain string body has no media",
			adf:  "just text",
			want: map[string]bool{},
		},
		{
			name: "media node without an id is ignored",
			adf:  map[string]any{"type": "media", "attrs": map[string]any{"alt": "no id"}},
			want: map[string]bool{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := CollectADFMediaIDs(tt.adf)
			if len(got) != len(tt.want) {
				t.Fatalf("CollectADFMediaIDs() = %v, want %v", got, tt.want)
			}
			for id := range tt.want {
				if !got[id] {
					t.Errorf("missing media id %q in %v", id, got)
				}
			}
		})
	}
}

func TestIsADFDocument(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value any
		want  bool
	}{
		{name: "doc map", value: map[string]any{"type": "doc", "content": []any{}}, want: true},
		{name: "non-doc node", value: map[string]any{"type": "paragraph"}, want: false},
		{name: "plain string", value: "text", want: false},
		{name: "option object", value: map[string]any{"value": "Red"}, want: false},
		{name: "nil", value: nil, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsADFDocument(tt.value); got != tt.want {
				t.Fatalf("IsADFDocument(%#v) = %t, want %t", tt.value, got, tt.want)
			}
		})
	}
}
