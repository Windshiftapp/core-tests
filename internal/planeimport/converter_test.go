package planeimport

import (
	"strings"
	"testing"
)

func TestConvertDescriptionJSON(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
		ok   bool
	}{
		{
			name: "empty document",
			in:   ``,
			want: "",
			ok:   true,
		},
		{
			name: "plain doc from the description_html column flavour",
			in:   `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"hello"}]}]}`,
			want: "hello",
			ok:   true,
		},
		{
			name: "editor-paragraph-block wrapper",
			in:   `{"type":"doc","content":[{"type":"editor-paragraph-block","content":[{"type":"paragraph","content":[{"type":"text","text":"hi there"}]}]}]}`,
			want: "hi there",
			ok:   true,
		},
		{
			name: "heading with marks",
			in:   `{"type":"doc","content":[{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"Title "},{"type":"text","marks":[{"type":"bold"}],"text":"bold"}]}]}`,
			want: "## Title **bold**",
			ok:   true,
		},
		{
			name: "code block keeps language",
			in:   "{\"type\":\"doc\",\"content\":[{\"type\":\"codeBlock\",\"attrs\":{\"language\":\"go\"},\"content\":[{\"type\":\"text\",\"text\":\"fmt.Println(1)\"}]}]}",
			want: "```go\nfmt.Println(1)\n```",
			ok:   true,
		},
		{
			name: "code block with embedded fence gets a longer fence",
			in:   "{\"type\":\"doc\",\"content\":[{\"type\":\"codeBlock\",\"content\":[{\"type\":\"text\",\"text\":\"```sh\\nx\\n```\"}]}]}",
			want: "````\n```sh\nx\n```\n````",
			ok:   true,
		},
		{
			name: "double-encoded document unwraps one level",
			in:   `"{\"type\":\"doc\",\"content\":[{\"type\":\"paragraph\",\"content\":[{\"type\":\"text\",\"text\":\"nested\"}]}]}"`,
			want: "nested",
			ok:   true,
		},
		{
			name: "unordered list",
			in:   `{"type":"doc","content":[{"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"one"}]}]},{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"two"}]}]}]}]}`,
			want: "- one\n- two",
			ok:   true,
		},
		{
			name: "task list",
			in:   `{"type":"doc","content":[{"type":"taskList","content":[{"type":"taskItem","attrs":{"checked":true},"content":[{"type":"paragraph","content":[{"type":"text","text":"done thing"}]}]},{"type":"taskItem","content":[{"type":"paragraph","content":[{"type":"text","text":"open thing"}]}]}]}]}`,
			want: "- [x] done thing\n- [ ] open thing",
			ok:   true,
		},
		{
			name: "blockquote",
			in:   `{"type":"doc","content":[{"type":"blockquote","content":[{"type":"paragraph","content":[{"type":"text","text":"quoted"}]}]}]}`,
			want: "> quoted",
			ok:   true,
		},
		{
			name: "horizontal rule",
			in:   `{"type":"doc","content":[{"type":"horizontalRule"}]}`,
			want: "---",
			ok:   true,
		},
		{
			name: "link mark becomes a markdown link",
			in:   `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","marks":[{"type":"link","attrs":{"href":"https://example.com"}}],"text":"click"}]}]}`,
			want: "[click](https://example.com)",
			ok:   true,
		},
		{
			name: "mention becomes an at-label the milkdown chip plugin scans",
			in:   `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"ping "},{"type":"mention","attrs":{"label":"Oskars","id":"u1"},"content":[{"type":"text","text":"Oskars"}]}]}]}`,
			want: "ping @ Oskars",
			ok:   true,
		},
		{
			name: "unknown node shape signals fallback",
			in:   `{"type":"doc","content":[{"type":"chartWidget","content":[]}]}`,
			want: "",
			ok:   false,
		},
		{
			name: "garbage is not a document",
			in:   `not json`,
			want: "",
			ok:   false,
		},
		{
			name: "empty paragraph renders nothing but stays ok",
			in:   `{"type":"doc","content":[{"type":"paragraph"}]}`,
			want: "",
			ok:   true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ConvertDescriptionJSON(tc.in)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v (markdown=%q)", ok, tc.ok, got)
			}
			if tc.ok && got != tc.want {
				t.Errorf("markdown =\n%q\nwant\n%q", got, tc.want)
			}
		})
	}
}

func TestConvertDescriptionJSON_Table(t *testing.T) {
	in := `{"type":"doc","content":[{"type":"table","attrs":{"columnWidths":[33,33],"width":100},"content":[` +
		`{"type":"tableRow","content":[{"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"col a"}]}]},{"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"col b"}]}]}]},` +
		`{"type":"tableRow","content":[{"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"1"}]}]},{"type":"tableCell","content":[{"type":"paragraph","content":[{"type":"text","text":"2"}]}]}]}]}]}`
	got, ok := ConvertDescriptionJSON(in)
	if !ok {
		t.Fatalf("table conversion failed: %q", got)
	}
	want := "| col a | col b |\n|---|---|\n| 1 | 2 |"
	if got != want {
		t.Errorf("table =\n%q\nwant\n%q", got, want)
	}
}

func TestConvertDescriptionJSON_ListWithNestedBlocks(t *testing.T) {
	in := `{"type":"doc","content":[{"type":"bulletList","content":[{"type":"listItem","content":[` +
		`{"type":"paragraph","content":[{"type":"text","text":"outer"}]},` +
		`{"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"inner"}]}]}]}]}]}]}`
	got, ok := ConvertDescriptionJSON(in)
	if !ok {
		t.Fatalf("nested list conversion failed")
	}
	if !strings.Contains(got, "- outer") || !strings.Contains(got, "  - inner") {
		t.Errorf("nested list shape wrong:\n%s", got)
	}
}

func TestStoryPointsFromEstimate(t *testing.T) {
	five := "5"
	empty := ""
	bogus := "large-ish"
	if v := storyPointsFromEstimate(nil); v != nil {
		t.Errorf("nil estimate mapped to %v", *v)
	}
	if v := storyPointsFromEstimate(&empty); v != nil {
		t.Errorf("empty estimate mapped to %v", *v)
	}
	if v := storyPointsFromEstimate(&bogus); v != nil {
		t.Errorf("unparseable estimate invented %v", *v)
	}
	if v := storyPointsFromEstimate(&five); v == nil || *v != 5 {
		t.Errorf("estimate 5 mapped wrong: %v", v)
	}
}

func TestEscapeMarkdownTextProtectsStructure(t *testing.T) {
	got := escapeMarkdownText("- not a list\nplain text stays")
	if !strings.HasPrefix(got, `\- not a list`) {
		t.Errorf("leading list marker not escaped: %q", got)
	}
	if !strings.HasSuffix(got, "plain text stays") {
		t.Errorf("ordinary text was mangled: %q", got)
	}
}
