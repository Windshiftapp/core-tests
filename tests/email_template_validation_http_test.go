package tests

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// TestEmailTemplateUpdateRejectsUnrenderableTemplate covers the save-time
// validation added for the bug hunt: a template that cannot render must be
// rejected with 400 and must not overwrite the stored row.
func TestEmailTemplateUpdateRejectsUnrenderableTemplate(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	type templateDoc struct {
		ID       int    `json:"id"`
		Name     string `json:"name"`
		Subject  string `json:"subject"`
		HTMLBody string `json:"html_body"`
		TextBody string `json:"text_body"`
		IsActive bool   `json:"is_active"`
	}

	list := MakeAuthRequest(t, server, http.MethodGet, "/email-templates", nil)
	defer list.Body.Close()
	AssertStatusCode(t, list, http.StatusOK)
	var templates []templateDoc
	DecodeJSON(t, list, &templates)

	var magicLink templateDoc
	for _, tpl := range templates {
		if tpl.Name == "magic_link" {
			magicLink = tpl
			break
		}
	}
	if magicLink.ID == 0 {
		t.Fatal("magic_link template was not seeded")
	}

	endpoint := fmt.Sprintf("/email-templates/%d", magicLink.ID)

	broken := MakeAuthRequest(t, server, http.MethodPut, endpoint, map[string]any{
		"subject":     magicLink.Subject,
		"html_body":   "<p>{{.FirstName</p>",
		"text_body":   magicLink.TextBody,
		"description": "",
		"is_active":   true,
	})
	defer broken.Body.Close()
	AssertStatusCode(t, broken, http.StatusBadRequest)

	after := MakeAuthRequest(t, server, http.MethodGet, endpoint, nil)
	defer after.Body.Close()
	AssertStatusCode(t, after, http.StatusOK)
	var stored templateDoc
	DecodeJSON(t, after, &stored)
	if stored.HTMLBody != magicLink.HTMLBody {
		t.Fatalf("rejected save mutated the stored template: %q", stored.HTMLBody)
	}

	valid := MakeAuthRequest(t, server, http.MethodPut, endpoint, map[string]any{
		"subject":     magicLink.Subject,
		"html_body":   "<p>Hi {{.FirstName}}, this link is valid for {{.ExpiresIn}}.</p>",
		"text_body":   "Hi {{.FirstName}}, this link is valid for {{.ExpiresIn}}.",
		"description": "",
		"is_active":   true,
	})
	defer valid.Body.Close()
	AssertStatusCode(t, valid, http.StatusOK)

	previewBroken := MakeAuthRequest(t, server, http.MethodPost, "/email-templates/preview", map[string]any{
		"name":      "magic_link",
		"subject":   "Sign in",
		"html_body": "<p>{{.FirstName</p>",
		"text_body": "body",
	})
	defer previewBroken.Body.Close()
	AssertStatusCode(t, previewBroken, http.StatusBadRequest)
}

// The intake receipt template is seeded and previewable with sample data,
// including the optional portal request link.
func TestIntakeConfirmationTemplateSeededAndPreviewable(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	type templateDoc struct {
		ID       int    `json:"id"`
		Name     string `json:"name"`
		Subject  string `json:"subject"`
		HTMLBody string `json:"html_body"`
		TextBody string `json:"text_body"`
		IsActive bool   `json:"is_active"`
	}

	list := MakeAuthRequest(t, server, http.MethodGet, "/email-templates", nil)
	defer list.Body.Close()
	AssertStatusCode(t, list, http.StatusOK)
	var templates []templateDoc
	DecodeJSON(t, list, &templates)

	var confirmation templateDoc
	for _, tpl := range templates {
		if tpl.Name == "intake_confirmation" {
			confirmation = tpl
			break
		}
	}
	if confirmation.ID == 0 {
		t.Fatal("intake_confirmation template was not seeded")
	}

	preview := MakeAuthRequest(t, server, http.MethodPost, "/email-templates/preview", map[string]any{
		"name":      "intake_confirmation",
		"subject":   confirmation.Subject,
		"html_body": confirmation.HTMLBody,
		"text_body": confirmation.TextBody,
	})
	defer preview.Body.Close()
	AssertStatusCode(t, preview, http.StatusOK)
	var rendered struct {
		Subject  string `json:"subject"`
		HTMLBody string `json:"html_body"`
		TextBody string `json:"text_body"`
	}
	DecodeJSON(t, preview, &rendered)
	if !strings.Contains(rendered.HTMLBody, "ACME-42") {
		t.Fatalf("preview HTML missing the sample item key:\n%s", rendered.HTMLBody)
	}
	if !strings.Contains(rendered.HTMLBody, "/portal/acme?view=requests") {
		t.Fatalf("preview HTML missing the sample portal link:\n%s", rendered.HTMLBody)
	}
}

// The dedicated mention template is seeded and previewable with sample data,
// including the actor, item key, message body, and deep link.
func TestMentionTemplateSeededAndPreviewable(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	type templateDoc struct {
		ID       int    `json:"id"`
		Name     string `json:"name"`
		Subject  string `json:"subject"`
		HTMLBody string `json:"html_body"`
		TextBody string `json:"text_body"`
		IsActive bool   `json:"is_active"`
	}

	list := MakeAuthRequest(t, server, http.MethodGet, "/email-templates", nil)
	defer list.Body.Close()
	AssertStatusCode(t, list, http.StatusOK)
	var templates []templateDoc
	DecodeJSON(t, list, &templates)

	var mention templateDoc
	for _, tpl := range templates {
		if tpl.Name == "mention" {
			mention = tpl
			break
		}
	}
	if mention.ID == 0 {
		t.Fatal("mention template was not seeded")
	}

	preview := MakeAuthRequest(t, server, http.MethodPost, "/email-templates/preview", map[string]any{
		"name":      "mention",
		"subject":   mention.Subject,
		"html_body": mention.HTMLBody,
		"text_body": mention.TextBody,
	})
	defer preview.Body.Close()
	AssertStatusCode(t, preview, http.StatusOK)
	var rendered struct {
		Subject  string `json:"subject"`
		HTMLBody string `json:"html_body"`
		TextBody string `json:"text_body"`
	}
	DecodeJSON(t, preview, &rendered)
	for _, want := range []string{"Sam Patel", "ACME-42", "token refresh path", "/workspaces/1/items/42"} {
		if !strings.Contains(rendered.HTMLBody, want) {
			t.Fatalf("preview HTML missing %q:\n%s", want, rendered.HTMLBody)
		}
	}
	if !strings.Contains(rendered.Subject, "Sam Patel mentioned you in ACME-42") {
		t.Fatalf("preview subject = %q", rendered.Subject)
	}
}
