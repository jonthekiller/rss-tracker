package feed

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseAtomFeed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>Example Feed</title>
  <entry>
    <id>urn:uuid:1</id>
    <title>First &amp; article</title>
    <link href="https://example.com/1" rel="alternate"/>
    <content type="html">&lt;p&gt;Hello &lt;b&gt;world&lt;/b&gt;&lt;/p&gt;</content>
    <author><name>John</name></author>
    <published>2024-01-01T10:00:00Z</published>
  </entry>
</feed>`))
	}))
	defer server.Close()

	result, err := FetchFeed(context.Background(), server.URL, FetchOptions{})
	if err != nil {
		t.Fatalf("FetchFeed: %v", err)
	}
	if result.Status != StatusOK || len(result.Articles) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	article := result.Articles[0]
	if article.Title != "First & article" {
		t.Errorf("title = %q", article.Title)
	}
	if article.Key != "urn:uuid:1" {
		t.Errorf("key = %q, want guid", article.Key)
	}
	if article.Summary != "Hello world" {
		t.Errorf("summary = %q", article.Summary)
	}
	if article.Author == nil || *article.Author != "John" {
		t.Errorf("author = %v", article.Author)
	}
	if article.PublishedAt == nil || article.PublishedAt.UTC().Year() != 2024 {
		t.Errorf("publishedAt = %v", article.PublishedAt)
	}
	if article.Image == nil || !strings.Contains(*article.Image, "example.com") {
		// content has no <img>: image should be nil
		if article.Image != nil {
			t.Errorf("image = %v, want nil", *article.Image)
		}
	}
	if result.Meta.Title == nil || *result.Meta.Title != "Example Feed" {
		t.Errorf("meta = %v", result.Meta)
	}
}

func TestParseRSSContentEncoded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<?xml version="1.0"?>
<rss version="2.0" xmlns:content="http://purl.org/rss/1.0/modules/content/">
  <channel><title>RSS Feed</title>
    <item>
      <guid>g1</guid>
      <title>RSS item</title>
      <link>https://example.com/rss</link>
      <description>short</description>
      <content:encoded><![CDATA[<p>Full <b>content</b> <img src="https://example.com/img.png"/>]]></content:encoded>
      <enclosure url="https://example.com/enc.png" type="image/png" length="1"/>
      <pubDate>Mon, 01 Jan 2024 10:00:00 GMT</pubDate>
    </item>
  </channel>
</rss>`))
	}))
	defer server.Close()

	result, err := FetchFeed(context.Background(), server.URL, FetchOptions{})
	if err != nil {
		t.Fatalf("FetchFeed: %v", err)
	}
	article := result.Articles[0]
	if article.Description == "" || !strings.Contains(article.Description, "Full") {
		t.Errorf("description should come from content:encoded, got %q", article.Description)
	}
	if article.Image == nil || *article.Image != "https://example.com/enc.png" {
		t.Errorf("image should come from enclosure, got %v", article.Image)
	}
}

func TestNotModified(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer server.Close()

	result, err := FetchFeed(context.Background(), server.URL, FetchOptions{})
	if err != nil {
		t.Fatalf("FetchFeed: %v", err)
	}
	if result.Status != StatusNotModified {
		t.Errorf("status = %v, want not_modified", result.Status)
	}
}

func TestConditionalHeaders(t *testing.T) {
	var etagHeader, lastModifiedHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		etagHeader = r.Header.Get("If-None-Match")
		lastModifiedHeader = r.Header.Get("If-Modified-Since")
		w.Write([]byte(`<rss version="2.0"><channel><title>t</title></channel></rss>`))
	}))
	defer server.Close()

	etag := `"abc123"`
	lastModified := "Mon, 01 Jan 2024 00:00:00 GMT"
	_, _ = FetchFeed(context.Background(), server.URL, FetchOptions{ETag: &etag, LastModified: &lastModified})
	if etagHeader != etag || lastModifiedHeader != lastModified {
		t.Errorf("conditional headers not sent: %q / %q", etagHeader, lastModifiedHeader)
	}
}

func TestHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	_, err := FetchFeed(context.Background(), server.URL, FetchOptions{})
	fetchErr, ok := err.(*FetchError)
	if !ok {
		t.Fatalf("want FetchError, got %T: %v", err, err)
	}
	if fetchErr.StatusCode != http.StatusNotFound {
		t.Errorf("status code = %d", fetchErr.StatusCode)
	}
}

func TestArticleKey(t *testing.T) {
	pub := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	if ArticleKey("g", "l", "t", &pub) != "g" {
		t.Error("guid must win")
	}
	if ArticleKey("", "l", "t", &pub) != "l" {
		t.Error("link must be second")
	}
	fallback := ArticleKey("", "", "title", &pub)
	if len(fallback) != 40 { // sha1 hex
		t.Errorf("fallback key = %q", fallback)
	}
	if fallback != HashKey("title|2024-01-01T10:00:00.000Z") {
		t.Errorf("fallback key mismatch: %q", fallback)
	}
}

func TestStripHTMLAndTruncate(t *testing.T) {
	got := StripHTML("<style>x</style><script>y</script><p>Hello&nbsp;<b>world</b></p>")
	if got != "Hello world" {
		t.Errorf("StripHTML = %q", got)
	}
	if Truncate("abcdef", 5) != "abcd…" {
		t.Errorf("Truncate = %q", Truncate("abcdef", 5))
	}
	long := strings.Repeat("a", 100)
	if got := Truncate(long, 300); got != long {
		t.Error("Truncate must not shorten below the limit")
	}
}
