package llm

import "testing"

func TestClassifyError(t *testing.T) {
	cases := []struct {
		name string
		msg  string
		want ErrorClass
	}{
		{
			name: "gemini deprecated model 404",
			msg:  `LLM API error: status 404 - [{"error":{"code":404,"message":"This model models/gemini-3.1-flash-lite-preview is no longer available","status":"NOT_FOUND"}}]`,
			want: ErrorClassModelNotFound,
		},
		{
			name: "openai 401",
			msg:  `LLM API error: status 401 - invalid api key`,
			want: ErrorClassAuthFailed,
		},
		{
			name: "rate limit",
			msg:  `LLM API error: status 429 - too many requests`,
			want: ErrorClassRateLimited,
		},
		{
			name: "server error",
			msg:  `LLM API error: status 502 - bad gateway`,
			want: ErrorClassServerError,
		},
		{
			name: "connection failed",
			msg:  `failed to connect to LLM service: dial tcp: i/o timeout`,
			want: ErrorClassConnectionFailed,
		},
		{
			name: "no status, no connection wording",
			msg:  `unexpected end of JSON input`,
			want: ErrorClassOther,
		},
		{
			name: "404 without model in body",
			msg:  `LLM API error: status 404 - page not found`,
			want: ErrorClassOther,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyError(tc.msg)
			if got != tc.want {
				t.Errorf("ClassifyError(%q) = %q, want %q", tc.msg, got, tc.want)
			}
		})
	}
}
