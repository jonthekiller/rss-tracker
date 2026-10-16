package feed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const enisaLikeJSON = `[
  {
    "id": "EUVD-2026-96307",
    "description": "Out-of-bounds read in <b>Apache DataSketches</b>.",
    "datePublished": "Oct 10, 2026, 10:23:20 AM",
    "baseScore": 9.8,
    "assigner": "apache"
  },
  {
    "id": "EUVD-2026-96306",
    "description": "Heap buffer overflow in the HLL sketch deserialization.",
    "datePublished": "Oct 10, 2026, 10:22:55 AM",
    "baseScore": 9.8,
    "assigner": "apache"
  }
]`

const wrappedJSON = `{"metadata":{"total":2},"data":{"items":[
  {"id": "CVE-1", "name": "First vuln", "url": "/cve/1", "description": "desc one", "published": "2026-10-10T10:00:00Z"},
  {"id": "CVE-2", "name": "Second vuln", "url": "/cve/2", "description": "desc two", "published": "2026-10-11T10:00:00Z"}
]}}`

func serve(t *testing.T, body string, contentType string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return server
}

func jsonConfig(specs map[string]string) ExtractConfig {
	return ExtractConfig{
		GUIDField:        specs["guid"],
		TitleField:       specs["title"],
		LinkField:        specs["link"],
		DescriptionField: specs["description"],
		PublishedField:   specs["published"],
		DateLayout:       specs["dateLayout"],
		ItemPath:         specs["itemPath"],
	}
}

func TestWatchENISALikeZeroConfig(t *testing.T) {
	server := serve(t, enisaLikeJSON, "application/json")
	result, err := FetchWatch(context.Background(), server.URL, SourceAuto, ExtractConfig{}, FetchOptions{})
	if err != nil {
		t.Fatalf("FetchWatch: %v", err)
	}
	if result.DetectedType != SourceJSON {
		t.Errorf("DetectedType = %q, want json", result.DetectedType)
	}
	if len(result.Articles) != 2 {
		t.Fatalf("articles = %d, want 2", len(result.Articles))
	}
	first := result.Articles[0]
	if first.GUID == nil || *first.GUID != "EUVD-2026-96307" {
		t.Errorf("GUID = %v", first.GUID)
	}
	if first.Title != "EUVD-2026-96307" {
		t.Errorf("Title = %q", first.Title)
	}
	// ENISA date format: Oct 10, 2026, 10:23:20 AM
	if first.PublishedAt == nil {
		t.Fatal("PublishedAt is nil, date was not parsed")
	}
	want := time.Date(2026, 10, 10, 10, 23, 20, 0, time.UTC)
	if !first.PublishedAt.UTC().Equal(want) {
		t.Errorf("PublishedAt = %v, want %v", first.PublishedAt.UTC(), want)
	}
	if !strings.Contains(first.Summary, "Out-of-bounds read in") || !strings.Contains(first.Summary, "Apache DataSketches") {
		t.Errorf("Summary = %q (HTML should be stripped)", first.Summary)
	}
	if first.Summary == "" || len(first.Summary) > 300 {
		t.Errorf("Summary = %q", first.Summary)
	}
}

func TestWatchJSONPatternTitleAndItemPath(t *testing.T) {
	server := serve(t, wrappedJSON, "application/json")
	cfg := jsonConfig(map[string]string{
		"itemPath":  "data.items",
		"title":     "{id} — {name}",
		"link":      "url",
		"published": "published",
	})
	result, err := FetchWatch(context.Background(), server.URL, SourceJSON, cfg, FetchOptions{})
	if err != nil {
		t.Fatalf("FetchWatch: %v", err)
	}
	if len(result.Articles) != 2 {
		t.Fatalf("articles = %d, want 2", len(result.Articles))
	}
	if result.Articles[0].Title != "CVE-1 — First vuln" {
		t.Errorf("Title = %q", result.Articles[0].Title)
	}
	if result.Articles[0].Link == nil || !strings.HasSuffix(*result.Articles[0].Link, "/cve/1") {
		t.Errorf("Link = %v (relative URL must resolve against the request URL)", result.Articles[0].Link)
	}
	if result.Articles[0].PublishedAt == nil {
		t.Error("PublishedAt should parse ISO dates")
	}
}

func TestWatchJSONKeyStability(t *testing.T) {
	server := serve(t, enisaLikeJSON, "application/json")
	a, err := FetchWatch(context.Background(), server.URL, SourceJSON, ExtractConfig{}, FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := FetchWatch(context.Background(), server.URL, SourceJSON, ExtractConfig{}, FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for i := range a.Articles {
		if a.Articles[i].Key != b.Articles[i].Key {
			t.Errorf("key %d unstable: %q vs %q", i, a.Articles[i].Key, b.Articles[i].Key)
		}
	}
}

func TestWatchJSONNoGuidFallsBackToItemHash(t *testing.T) {
	body := `[{"name":"only-name","description":"d"}]`
	server := serve(t, body, "application/json")
	result, err := FetchWatch(context.Background(), server.URL, SourceJSON, ExtractConfig{}, FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Articles) != 1 || result.Articles[0].Title != "only-name" {
		t.Fatalf("articles = %+v", result.Articles)
	}
	if result.Articles[0].Key == "" {
		t.Error("key must fall back to the item hash")
	}
}

func TestWatchJSONItemPathNotFound(t *testing.T) {
	server := serve(t, `{"foo":1}`, "application/json")
	_, err := FetchWatch(context.Background(), server.URL, SourceJSON, ExtractConfig{ItemPath: "missing"}, FetchOptions{})
	if err == nil || !strings.Contains(err.Error(), "itemPath") {
		t.Fatalf("err = %v, want itemPath error", err)
	}
}

func TestWatchHTMLSelectors(t *testing.T) {
	html := `<html><body>
	<ul id="vulns">
	<li class="vuln"><a href="/advisory/1">Advisory one</a><p class="desc">First description</p><time datetime="2026-10-10">Oct 10, 2026</time></li>
	<li class="vuln"><a href="/advisory/2">Advisory two</a><p class="desc">Second description</p><time datetime="2026-10-11">Oct 11, 2026</time></li>
	</ul></body></html>`
	server := serve(t, html, "text/html")
	cfg := ExtractConfig{
		ItemSelector:        "li.vuln",
		LinkSelector:        "a@href",
		DescriptionSelector: "p.desc",
		DateSelector:        "time@datetime",
	}
	// TitleSelector unset: falls back to the link text.
	result, err := FetchWatch(context.Background(), server.URL, SourceHTML, cfg, FetchOptions{})
	if err != nil {
		t.Fatalf("FetchWatch: %v", err)
	}
	if len(result.Articles) != 2 {
		t.Fatalf("articles = %d, want 2", len(result.Articles))
	}
	if result.Articles[0].Title != "Advisory one" {
		t.Errorf("Title = %q", result.Articles[0].Title)
	}
	if result.Articles[0].Link == nil || !strings.HasSuffix(*result.Articles[0].Link, "/advisory/1") {
		t.Errorf("Link = %v", result.Articles[0].Link)
	}
	if result.Articles[0].Description != "First description" {
		t.Errorf("Description = %q", result.Articles[0].Description)
	}
	if result.Articles[0].PublishedAt == nil {
		t.Error("date selector should populate PublishedAt")
	}
	if result.Articles[0].Key == "" {
		t.Error("key should derive from link/title")
	}
}

func TestWatchHTMLPageDiffMode(t *testing.T) {
	body := `<html><head><script>var rotating = 12345;</script><style>.x{color:red}</style></head>
	<body><h1>Status page</h1><p>Version 1.2.3</p></body></html>`
	server := serve(t, body, "text/html")
	result, err := FetchWatch(context.Background(), server.URL, SourceHTML, ExtractConfig{}, FetchOptions{})
	if err != nil {
		t.Fatalf("FetchWatch: %v", err)
	}
	if len(result.Articles) != 1 {
		t.Fatalf("articles = %d, want 1", len(result.Articles))
	}
	article := result.Articles[0]
	if article.Title != "Page updated" {
		t.Errorf("Title = %q", article.Title)
	}
	if strings.Contains(article.Description, "rotating") || strings.Contains(article.Description, "color") {
		t.Errorf("script/style content leaked into the diff: %q", article.Description)
	}
	if !strings.Contains(article.Description, "Version 1.2.3") {
		t.Errorf("Description = %q", article.Description)
	}
	// Same content → same key (no re-notification).
	again, err := FetchWatch(context.Background(), server.URL, SourceHTML, ExtractConfig{}, FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if again.Articles[0].Key != article.Key {
		t.Error("identical pages must produce the same diff key")
	}
	// Changed content → new key.
	changed := strings.Replace(body, "1.2.3", "1.2.4", 1)
	server2 := serve(t, changed, "text/html")
	other, err := FetchWatch(context.Background(), server2.URL, SourceHTML, ExtractConfig{}, FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if other.Articles[0].Key == article.Key {
		t.Error("changed page must produce a new diff key")
	}
}

func TestWatchConditionalGet304(t *testing.T) {
	var requestCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if r.Header.Get("If-None-Match") == `"abc"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"abc"`)
		w.Write([]byte(enisaLikeJSON))
	}))
	t.Cleanup(server.Close)

	first, err := FetchWatch(context.Background(), server.URL, SourceJSON, ExtractConfig{}, FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != StatusOK || len(first.Articles) != 2 {
		t.Fatalf("first fetch = %+v", first)
	}
	second, err := FetchWatch(context.Background(), server.URL, SourceJSON, ExtractConfig{}, FetchOptions{ETag: first.ETag})
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != StatusNotModified {
		t.Errorf("second status = %v, want not_modified", second.Status)
	}
	if requestCount != 2 {
		t.Errorf("requestCount = %d, want 2", requestCount)
	}
}

func TestParseExtractConfig(t *testing.T) {
	if _, err := ParseExtractConfig(nil); err != nil {
		t.Errorf("nil config: %v", err)
	}
	valid := `{"itemPath":"data.items","titleField":"{id} — {name}","itemSelector":"li.vuln"}`
	if _, err := ParseExtractConfig(&valid); err != nil {
		t.Errorf("valid config: %v", err)
	}
	unknown := `{"nope":1}`
	if _, err := ParseExtractConfig(&unknown); err == nil {
		t.Error("unknown fields must be rejected")
	}
	malformed := `{nope`
	if _, err := ParseExtractConfig(&malformed); err == nil {
		t.Error("malformed JSON must be rejected")
	}
}

func TestExtractConfigRoundTrip(t *testing.T) {
	cfg := ExtractConfig{ItemPath: "data.items", TitleField: "{id}"}
	encoded := cfg.Encode()
	if encoded == nil {
		t.Fatal("Encode returned nil for a non-empty config")
	}
	decoded, err := ParseExtractConfig(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.Extras) != 0 || decoded.ItemPath != cfg.ItemPath || decoded.TitleField != cfg.TitleField {
		t.Errorf("decoded = %+v, want %+v", decoded, cfg)
	}
	empty := ExtractConfig{}
	if empty.Encode() != nil {
		t.Error("empty config must encode to nil")
	}
	withExtras := ExtractConfig{Extras: map[string]string{"score": "baseScore"}}
	if withExtras.Encode() == nil {
		t.Error("extras-only config must encode")
	}
}

func TestDetectFeedLink(t *testing.T) {
	html := `<html><head>
	<link rel="stylesheet" href="/style.css">
	<link rel="alternate" type="application/rss+xml" title="Blog" href="https://example.com/feed.xml">
	</head><body>hi</body></html>`
	link := DetectFeedLink([]byte(html), "https://example.com/blog/")
	if link == nil {
		t.Fatal("DetectFeedLink returned nil")
	}
	if *link != "https://example.com/feed.xml" {
		t.Errorf("link = %q", *link)
	}
	if DetectFeedLink([]byte("<html><body>no feed</body></html>"), "https://example.com") != nil {
		t.Error("pages without a feed link must return nil")
	}
}

func TestSniffSourceType(t *testing.T) {
	if got := SniffSourceType([]byte(enisaLikeJSON), ExtractConfig{}); got != SourceJSON {
		t.Errorf("SniffSourceType(json) = %q", got)
	}
	if got := SniffSourceType([]byte("<html><body>x</body></html>"), ExtractConfig{}); got != SourceHTML {
		t.Errorf("SniffSourceType(html) = %q", got)
	}
	// Invalid JSON pretending to be JSON falls back to HTML.
	if got := SniffSourceType([]byte("{not json"), ExtractConfig{}); got != SourceHTML {
		t.Errorf("SniffSourceType(broken) = %q", got)
	}
}

func TestResolveURL(t *testing.T) {
	if got := resolveURL("https://ex.com/a/b", "/c"); got != "https://ex.com/c" {
		t.Errorf("resolveURL absolute path = %q", got)
	}
	if got := resolveURL("", "/c"); got != "/c" {
		t.Errorf("resolveURL without base = %q", got)
	}
	if got := resolveURL("https://ex.com", "https://other.com/x"); got != "https://other.com/x" {
		t.Errorf("absolute href must be kept: %q", got)
	}
}

func TestWatchJSONExtras(t *testing.T) {
	body := `[
	  {"id":"EUVD-1","description":"Desc","baseScore":9.8,
	   "assigner":"apache",
	   "enisaIdVendor":[{"vendor":{"name":"Apache Software Foundation"}}],
	   "aliases":["CVE-1","GHSA-x"]}
	]`
	server := serve(t, body, "application/json")
	cfg := ExtractConfig{Extras: map[string]string{
		"score":    "baseScore",
		"assigner": "assigner",
		"vendor":   "enisaIdVendor.0.vendor.name",
		"aliases":  "aliases",
		"link":     "https://euvd.enisa.europa.eu/vulnerability/{id}",
		"missing":  "no.such.path",
	}}
	result, err := FetchWatch(context.Background(), server.URL, SourceJSON, cfg, FetchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	article := result.Articles[0]
	if article.Extra == nil {
		t.Fatal("Extra is nil")
	}
	if article.Extra["score"] != "9.8" {
		t.Errorf("score = %q, want 9.8 (float formatting)", article.Extra["score"])
	}
	if article.Extra["assigner"] != "apache" {
		t.Errorf("assigner = %q", article.Extra["assigner"])
	}
	if article.Extra["vendor"] != "Apache Software Foundation" {
		t.Errorf("vendor = %q (nested path with array index)", article.Extra["vendor"])
	}
	if article.Extra["aliases"] != "CVE-1, GHSA-x" {
		t.Errorf("aliases = %q (arrays joined)", article.Extra["aliases"])
	}
	if article.Extra["link"] != "https://euvd.enisa.europa.eu/vulnerability/EUVD-1" {
		t.Errorf("link = %q (pattern with placeholder)", article.Extra["link"])
	}
	if v, ok := article.Extra["missing"]; !ok || v != "" {
		t.Errorf("missing path = %q, %v (must render empty)", v, ok)
	}
}

func TestParseExtractConfigExtraValidation(t *testing.T) {
	badName := `{"extras":{"Bad Name!":"x"}}`
	if _, err := ParseExtractConfig(&badName); err == nil || !strings.Contains(err.Error(), "extra name") {
		t.Errorf("err = %v, want invalid extra name", err)
	}
	reserved := `{"extras":{"title":"x"}}`
	if _, err := ParseExtractConfig(&reserved); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Errorf("err = %v, want reserved name", err)
	}
	valid := `{"extras":{"score":"baseScore","cvss3":"x"}}`
	if _, err := ParseExtractConfig(&valid); err != nil {
		t.Errorf("valid extras rejected: %v", err)
	}
}
