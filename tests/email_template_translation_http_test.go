package tests

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
)

// TestEmailTemplateTranslationCRUD covers the admin per-locale override
// endpoints: upsert, list, validation, and delete.
func TestEmailTemplateTranslationCRUD(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	type translationDoc struct {
		Locale   string `json:"locale"`
		Subject  string `json:"subject"`
		HTMLBody string `json:"html_body"`
		TextBody string `json:"text_body"`
	}
	type templateDoc struct {
		ID           int              `json:"id"`
		Name         string           `json:"name"`
		Subject      string           `json:"subject"`
		HTMLBody     string           `json:"html_body"`
		TextBody     string           `json:"text_body"`
		Translations []translationDoc `json:"translations"`
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

	translationURL := fmt.Sprintf("/email-templates/%d/translations/de", magicLink.ID)
	upsert := MakeAuthRequest(t, server, http.MethodPut, translationURL, map[string]any{
		"subject":   "DE Betreff",
		"html_body": "<p>DE Inhalt</p>",
		"text_body": "DE Inhalt",
	})
	defer upsert.Body.Close()
	AssertStatusCode(t, upsert, http.StatusOK)

	// The list now carries the override.
	list2 := MakeAuthRequest(t, server, http.MethodGet, "/email-templates", nil)
	defer list2.Body.Close()
	AssertStatusCode(t, list2, http.StatusOK)
	var templates2 []templateDoc
	DecodeJSON(t, list2, &templates2)
	found := false
	for _, tpl := range templates2 {
		if tpl.Name != "magic_link" {
			continue
		}
		for _, tr := range tpl.Translations {
			if tr.Locale == "de" && tr.Subject == "DE Betreff" {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("list did not include the de translation")
	}

	// Unsupported locale is rejected.
	unsupported := MakeAuthRequest(t, server, http.MethodPut,
		fmt.Sprintf("/email-templates/%d/translations/xx", magicLink.ID), map[string]any{
			"subject":   "x",
			"html_body": "<p>x</p>",
		})
	defer unsupported.Body.Close()
	AssertStatusCode(t, unsupported, http.StatusBadRequest)

	// An unrenderable translation is rejected.
	broken := MakeAuthRequest(t, server, http.MethodPut,
		fmt.Sprintf("/email-templates/%d/translations/fr", magicLink.ID), map[string]any{
			"subject":   "broken",
			"html_body": "<p>{{.FirstName</p>",
		})
	defer broken.Body.Close()
	AssertStatusCode(t, broken, http.StatusBadRequest)

	// Delete reverts the locale.
	del := MakeAuthRequest(t, server, http.MethodDelete, translationURL, nil)
	defer del.Body.Close()
	AssertStatusCode(t, del, http.StatusOK)

	delAgain := MakeAuthRequest(t, server, http.MethodDelete, translationURL, nil)
	defer delAgain.Body.Close()
	AssertStatusCode(t, delAgain, http.StatusNotFound)
}

// TestEmailTemplatePreviewRendersLocale proves the preview honors the locale,
// so an admin sees the override (or localized built-in default) they will ship.
func TestEmailTemplatePreviewRendersLocale(t *testing.T) {
	server, _ := StartTestServer(t, GetDBType())
	CreateBearerToken(t, server)

	type templateDoc struct {
		ID       int    `json:"id"`
		Name     string `json:"name"`
		Subject  string `json:"subject"`
		HTMLBody string `json:"html_body"`
		TextBody string `json:"text_body"`
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

	preview := MakeAuthRequest(t, server, http.MethodPost, "/email-templates/preview", map[string]any{
		"name":      "magic_link",
		"subject":   magicLink.Subject,
		"html_body": magicLink.HTMLBody,
		"text_body": magicLink.TextBody,
		"locale":    "de",
	})
	defer preview.Body.Close()
	AssertStatusCode(t, preview, http.StatusOK)
	var rendered struct {
		Subject  string `json:"subject"`
		HTMLBody string `json:"html_body"`
		TextBody string `json:"text_body"`
	}
	DecodeJSON(t, preview, &rendered)
	if !strings.Contains(rendered.HTMLBody, `lang="de"`) {
		t.Fatalf("preview is not rendered in de:\n%s", rendered.HTMLBody)
	}
	if !strings.Contains(rendered.HTMLBody, "Bei Ihrem Portal anmelden") {
		t.Fatalf("preview did not use the German built-in default:\n%s", rendered.HTMLBody)
	}
}
