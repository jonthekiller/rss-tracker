package render

import (
	"strings"
	"testing"
)

// extrasContext is testContext with custom JSON fields attached.
func extrasContext() TemplateContext {
	ctx := testContext()
	ctx.Article.Extra = map[string]string{
		"score":   "9.8",
		"vendor":  "Apache Software Foundation",
		"aliases": "CVE-2026-103513, GHSA-2w73-5h25-v3cg",
		"empty":   "",
	}
	return ctx
}

func TestRenderTemplateExtrasPlaceholders(t *testing.T) {
	got := RenderTemplate("**{title}** (CVSS {score}) — {vendor}\n{aliases}", extrasContext())
	for _, want := range []string{"CVSS 9.8", "Apache Software Foundation", "CVE-2026-103513, GHSA-2w73-5h25-v3cg"} {
		if !strings.Contains(got, want) {
			t.Errorf("rendered %q missing %q", got, want)
		}
	}
}

func TestRenderTemplateExtrasEmptyValue(t *testing.T) {
	got := RenderTemplate("A{empty}B", extrasContext())
	if got != "AB" {
		t.Errorf("rendered = %q, want %q (empty extra renders empty, not the literal)", got, "AB")
	}
}

func TestRenderTemplateStandardNamesWinOverExtras(t *testing.T) {
	ctx := extrasContext()
	ctx.Article.Extra["title"] = "SHADOWED"
	ctx.Article.Extra["feed.name"] = "SHADOWED"
	got := RenderTemplate("{title}|{feed.name}", ctx)
	if got != "Hello|My Feed" {
		t.Errorf("rendered = %q, standard placeholders must win", got)
	}
}

func TestRenderTemplateDigitsInPlaceholder(t *testing.T) {
	ctx := extrasContext()
	ctx.Article.Extra["cvss3"] = "high"
	if got := RenderTemplate("sev: {cvss3}", ctx); got != "sev: high" {
		t.Errorf("rendered = %q, digits must be allowed in placeholder names", got)
	}
}

func TestBuildArticlePayloadEmbedWithExtras(t *testing.T) {
	embedJSON := `{"title":"{title}","description":"{summary}","color":"#cc3333","timestamp":"article","fields":[{"name":"CVSS","value":"{score}","inline":true},{"name":"Vendor","value":"{vendor}","inline":true},{"name":"Aliases","value":"{aliases}"}]}`
	payload := BuildArticlePayload(DefaultTemplate, &embedJSON, extrasContext())
	if len(payload.Embeds) != 1 {
		t.Fatalf("embeds = %d", len(payload.Embeds))
	}
	fields := payload.Embeds[0].Fields
	if len(fields) != 3 {
		t.Fatalf("fields = %+v", fields)
	}
	if fields[0].Name != "CVSS" || fields[0].Value != "9.8" {
		t.Errorf("field 0 = %+v", fields[0])
	}
	if fields[1].Value != "Apache Software Foundation" || !fields[1].Inline {
		t.Errorf("field 1 = %+v", fields[1])
	}
	if fields[2].Value != "CVE-2026-103513, GHSA-2w73-5h25-v3cg" {
		t.Errorf("field 2 = %+v", fields[2])
	}
}

// TestRenderTemplateUnknownPlaceholderUntouchedWithExtras guards the
// pre-extras behavior: unmatched placeholders stay literal.
func TestRenderTemplateUnknownPlaceholderUntouchedWithExtras(t *testing.T) {
	got := RenderTemplate("{nope} {unknown_name}", extrasContext())
	if got != "{nope} {unknown_name}" {
		t.Errorf("rendered = %q, unknown placeholders must stay untouched", got)
	}
}
