package tests

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// validTestPDF returns a minimal PDF that http.DetectContentType recognizes as
// application/pdf. Content only needs to be sniffable; it is never rendered by
// the test.
func validTestPDF(t *testing.T) []byte {
	t.Helper()
	return []byte("%PDF-1.4\n" +
		"1 0 obj\n<< /Type /Catalog /Pages 2 0 R >>\nendobj\n" +
		"2 0 obj\n<< /Type /Pages /Kids [3 0 R] /Count 1 >>\nendobj\n" +
		"3 0 obj\n<< /Type /Page /Parent 2 0 R /MediaBox [0 0 200 200] >>\nendobj\n" +
		"trailer\n<< /Root 1 0 R >>\n%%EOF\n")
}

// TestItemAttachmentRenderAndDownloadEndpoints pins the split between the
// rendering endpoint (/content) and the forced-download endpoint (/download).
// The in-app PDF lightbox frames /content, so renderable media must be served
// inline there; /download must always force a download. Serving PDFs as
// "attachment" from /content left the preview iframe blank.
func TestItemAttachmentRenderAndDownloadEndpoints(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)
	workspaceID, _ := CreateTestWorkspace(t, server, "Attachment inline content", shortKey("AIC"))
	itemID := CreateTestItem(t, server, workspaceID, "Inline content item")

	tests := []struct {
		name       string
		filename   string
		data       []byte
		renderable bool
	}{
		{
			name:       "pdf is renderable",
			filename:   "pricing.pdf",
			data:       validTestPDF(t),
			renderable: true,
		},
		{
			name:       "png is renderable",
			filename:   "screenshot.png",
			data:       validTestPNG(t),
			renderable: true,
		},
		{
			name:       "json is not renderable",
			filename:   "diagram.json",
			data:       []byte(`{"elements":[],"appState":{}}`),
			renderable: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			upload := makeBearerMultipartRequest(t, server,
				fmt.Sprintf("/rest/api/v2/items/%d/attachments", itemID), nil, tc.filename, tc.data)
			defer upload.Body.Close()
			AssertStatusCode(t, upload, http.StatusCreated)

			var created struct {
				Data struct {
					ID int `json:"id"`
				} `json:"data"`
			}
			DecodeJSON(t, upload, &created)
			if created.Data.ID == 0 {
				t.Fatalf("upload response missing attachment id")
			}

			// /content is the rendering endpoint.
			content := MakeAuthRequest(t, server, http.MethodGet,
				fmt.Sprintf("/v2/attachments/%d/content", created.Data.ID), nil)
			defer content.Body.Close()
			AssertStatusCode(t, content, http.StatusOK)
			wantContentDisposition := "attachment"
			if tc.renderable {
				wantContentDisposition = "inline"
			}
			assertDispositionPrefix(t, content, wantContentDisposition)
			if tc.renderable {
				assertNoSandboxCSP(t, content)
			} else {
				assertSandboxCSP(t, content)
			}
			assertBody(t, content, tc.data)

			// /download always forces a download, even for renderable media.
			download := MakeAuthRequest(t, server, http.MethodGet,
				fmt.Sprintf("/v2/attachments/%d/download", created.Data.ID), nil)
			defer download.Body.Close()
			AssertStatusCode(t, download, http.StatusOK)
			assertDispositionPrefix(t, download, "attachment")
			assertSandboxCSP(t, download)
			assertBody(t, download, tc.data)
		})
	}
}

func assertDispositionPrefix(t *testing.T, response *http.Response, want string) {
	t.Helper()
	if got := response.Header.Get("Content-Disposition"); !strings.HasPrefix(got, want) {
		t.Errorf("Content-Disposition = %q, want %q prefix", got, want)
	}
}

func assertSandboxCSP(t *testing.T, response *http.Response) {
	t.Helper()
	if csp := response.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") {
		t.Errorf("Content-Security-Policy = %q, want sandbox directive", csp)
	}
}

func assertNoSandboxCSP(t *testing.T, response *http.Response) {
	t.Helper()
	if csp := response.Header.Get("Content-Security-Policy"); strings.Contains(csp, "sandbox") {
		t.Errorf("Content-Security-Policy = %q, want no sandbox directive for inline media", csp)
	}
}

func assertBody(t *testing.T, response *http.Response, want []byte) {
	t.Helper()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if !bytes.Equal(body, want) {
		t.Errorf("response body = %q, want %q", body, want)
	}
}
