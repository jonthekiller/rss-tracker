// Non-RSS sources: JSON APIs and HTML pages. FetchWatch normalizes them to
// the same []Article the RSS pipeline consumes, so detection, filters,
// digests, templates and seen-keys work unchanged.
package feed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// Source types stored in feeds.source_type.
const (
	SourceRSS  = "rss"
	SourceJSON = "json"
	SourceHTML = "html"
	SourceAuto = "auto"
)

// ExtractConfig carries the extraction rules for a non-RSS source. In JSON
// mode the *Field values are either a dot path into the item object or a
// pattern containing {path} placeholders. In HTML mode the *Selector values
// are CSS selectors relative to each item ("sel@attr" reads an attribute).
// An HTML config without ItemSelector means whole-page diff mode.
type ExtractConfig struct {
	ItemPath         string `json:"itemPath,omitempty"`
	GUIDField        string `json:"guidField,omitempty"`
	TitleField       string `json:"titleField,omitempty"`
	LinkField        string `json:"linkField,omitempty"`
	DescriptionField string `json:"descriptionField,omitempty"`
	PublishedField   string `json:"publishedField,omitempty"`
	DateLayout       string `json:"dateLayout,omitempty"`

	ItemSelector        string `json:"itemSelector,omitempty"`
	TitleSelector       string `json:"titleSelector,omitempty"`
	LinkSelector        string `json:"linkSelector,omitempty"`
	DescriptionSelector string `json:"descriptionSelector,omitempty"`
	DateSelector        string `json:"dateSelector,omitempty"`

	// Extras maps custom placeholder names to dot paths (or {placeholder}
	// patterns) inside each JSON item: {"score": "baseScore"} enables
	// {score} in message/embed templates. JSON sources only.
	Extras map[string]string `json:"extras,omitempty"`
}

// ParseExtractConfig decodes and validates an extract_config JSON blob
// (nil/empty means: no explicit config — defaults apply).
func ParseExtractConfig(raw *string) (ExtractConfig, error) {
	cfg := ExtractConfig{}
	if raw == nil || *raw == "" {
		return cfg, nil
	}
	dec := json.NewDecoder(strings.NewReader(*raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("invalid extraction config: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// reservedExtraNames collide with the standard template placeholders.
var reservedExtraNames = map[string]bool{
	"title": true, "link": true, "description": true, "summary": true,
	"author": true, "date": true, "image": true, "feed.name": true,
	"empty": true,
}

var extraNameRe = regexp.MustCompile(`^[a-z0-9_]+$`)

// validate checks that extra placeholder names are renderable and do not
// shadow the standard placeholders.
func (c ExtractConfig) validate() error {
	for name := range c.Extras {
		if !extraNameRe.MatchString(name) {
			return fmt.Errorf(
				"invalid extra name %q: lowercase letters, digits and underscore only (used as {name} in templates)", name)
		}
		if reservedExtraNames[name] {
			return fmt.Errorf("extra name %q is reserved by a standard placeholder", name)
		}
	}
	return nil
}

// isEmpty reports whether the config carries no explicit rule.
func (c ExtractConfig) isEmpty() bool {
	return c.ItemPath == "" && c.GUIDField == "" && c.TitleField == "" &&
		c.LinkField == "" && c.DescriptionField == "" && c.PublishedField == "" &&
		c.DateLayout == "" && c.ItemSelector == "" && c.TitleSelector == "" &&
		c.LinkSelector == "" && c.DescriptionSelector == "" && c.DateSelector == "" &&
		len(c.Extras) == 0
}

// Encode serializes the config for storage; nil when empty.
func (c ExtractConfig) Encode() *string {
	if c.isEmpty() {
		return nil
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return nil
	}
	s := string(raw)
	return &s
}

// Default field names tried (in order) when no explicit field is configured.
var (
	defaultGUIDFields        = []string{"id", "guid", "uuid"}
	defaultTitleFields       = []string{"title", "name", "id"}
	defaultLinkFields        = []string{"url", "link", "href", "html_url"}
	defaultDescriptionFields = []string{"description", "summary", "content", "body"}
	defaultPublishedFields   = []string{"datePublished", "publishedAt", "published", "date", "created", "updated"}
)

// dateLayouts are tried (in order) when DateLayout is not set.
var dateLayouts = []string{
	time.RFC3339Nano, time.RFC3339,
	"2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02",
	time.RFC1123Z, time.RFC1123,
	"Jan 2, 2006, 3:04:05 PM", "Jan 2, 2006 3:04:05 PM",
	"January 2, 2006", "2 Jan 2006",
	"2006/01/02 15:04:05", "2006/01/02",
}

// FetchWatch fetches a non-RSS source and returns normalized articles.
// sourceType is one of json/html/auto (auto sniffs the body). The shared
// conditional-GET semantics (ETag/304, 10 MB cap) apply like FetchFeed.
func FetchWatch(ctx context.Context, rawURL, sourceType string, cfg ExtractConfig, options FetchOptions) (*FetchFeedResult, error) {
	body, etag, lastModified, finalURL, err := httpGet(ctx, rawURL, "*/*", options)
	if err != nil {
		return nil, err
	}
	if body == nil {
		return &FetchFeedResult{Status: StatusNotModified}, nil
	}

	var articles []Article
	resolved := sourceType
	switch sourceType {
	case SourceJSON:
		articles, err = articlesFromJSON(body, cfg, finalURL)
	case SourceHTML, SourceAuto:
		if looksLikeJSON(body) {
			articles, err = articlesFromJSON(body, cfg, finalURL)
			if err != nil && sourceType == SourceAuto {
				articles, err = articlesFromHTML(body, cfg, finalURL)
			}
		} else {
			articles, err = articlesFromHTML(body, cfg, finalURL)
		}
		if sourceType == SourceAuto {
			resolved = SniffSourceType(body, cfg)
		}
	default:
		return nil, &FetchError{Message: "unknown source type: " + sourceType}
	}
	if err != nil {
		return nil, err
	}
	return &FetchFeedResult{
		Status:       StatusOK,
		Articles:     articles,
		ETag:         etag,
		LastModified: lastModified,
		DetectedType: resolved,
	}, nil
}

// FetchFeedLink downloads an HTML page and returns the absolute href of its
// first declared RSS/Atom <link>, or nil. Used at add time to suggest a real
// feed when the user adds a page that has one.
func FetchFeedLink(ctx context.Context, rawURL string, options FetchOptions) (*string, error) {
	body, _, _, finalURL, err := httpGet(ctx, rawURL, "text/html,*/*", options)
	if err != nil {
		return nil, err
	}
	if body == nil {
		return nil, nil
	}
	return DetectFeedLink(body, finalURL), nil
}

func looksLikeJSON(body []byte) bool {
	trimmed := bytes.TrimLeft(bytes.TrimPrefix(body, []byte("\xef\xbb\xbf")), " \t\r\n")
	return len(trimmed) > 0 && (trimmed[0] == '[' || trimmed[0] == '{')
}

// ---- JSON mode ----

func articlesFromJSON(body []byte, cfg ExtractConfig, baseURL string) ([]Article, error) {
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return nil, &FetchError{Message: "Not valid JSON: " + err.Error()}
	}
	items, err := locateItems(root, cfg.ItemPath)
	if err != nil {
		return nil, &FetchError{Message: err.Error()}
	}
	articles := make([]Article, 0, len(items))
	for i, item := range items {
		obj, ok := item.(map[string]any)
		if !ok {
			return nil, &FetchError{Message: fmt.Sprintf("item %d is not a JSON object", i)}
		}
		articles = append(articles, articleFromJSONItem(obj, cfg, baseURL, i))
	}
	return articles, nil
}

// locateItems resolves the array of items: explicit dot path, root array, or
// (best-effort) the first object-valued array of a root object.
func locateItems(root any, itemPath string) ([]any, error) {
	if itemPath != "" && itemPath != "$" {
		value := resolvePath(root, strings.Split(itemPath, "."))
		if value == nil {
			return nil, fmt.Errorf("itemPath %q not found in JSON document", itemPath)
		}
		return asArray(value)
	}
	if arr, ok := root.([]any); ok {
		return arr, nil
	}
	obj, ok := root.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("JSON root is neither an array nor an object; set itemPath")
	}
	// Prefer arrays of objects; remember any array as fallback.
	var fallback []any
	for _, key := range sortedKeys(obj) {
		arr, ok := obj[key].([]any)
		if !ok {
			continue
		}
		if len(fallback) == 0 {
			fallback = arr
		}
		if len(arr) > 0 {
			if _, isObj := arr[0].(map[string]any); isObj {
				return arr, nil
			}
		}
	}
	if fallback != nil {
		return fallback, nil
	}
	return nil, fmt.Errorf("no item array found in JSON document; set itemPath")
}

func asArray(value any) ([]any, error) {
	switch v := value.(type) {
	case []any:
		return v, nil
	case map[string]any:
		for _, key := range sortedKeys(v) {
			if arr, ok := v[key].([]any); ok {
				return arr, nil
			}
		}
		return nil, fmt.Errorf("itemPath points to an object without an array inside")
	default:
		return nil, fmt.Errorf("itemPath does not point to an array")
	}
}

func sortedKeys(obj map[string]any) []string {
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// resolvePath walks a dot path through maps and arrays (numeric segments).
func resolvePath(node any, path []string) any {
	for _, part := range path {
		switch n := node.(type) {
		case map[string]any:
			node = n[part]
		case []any:
			idx, err := strconv.Atoi(part)
			if err != nil || idx < 0 || idx >= len(n) {
				return nil
			}
			node = n[idx]
		default:
			return nil
		}
	}
	return node
}

// stringify converts a resolved JSON scalar to its display form.
func stringify(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(v)
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if s := stringify(item); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	default:
		raw, err := json.Marshal(v)
		if err != nil {
			return ""
		}
		return string(raw)
	}
}

var placeholderRe = regexp.MustCompile(`\{([^{}]+)\}`)

// fieldString resolves a field spec against a JSON item: a {placeholder}
// pattern is rendered from the item, otherwise the spec is a dot path.
func fieldString(item map[string]any, spec string) string {
	if spec == "" {
		return ""
	}
	if strings.Contains(spec, "{") {
		return placeholderRe.ReplaceAllStringFunc(spec, func(match string) string {
			path := strings.Split(match[1:len(match)-1], ".")
			return stringify(resolvePath(item, path))
		})
	}
	value := resolvePath(item, strings.Split(spec, "."))
	return stringify(value)
}

// firstField returns the first non-empty resolution among the candidate
// specs (explicit config first, then defaults).
func firstField(item map[string]any, explicit string, defaults []string) string {
	if explicit != "" {
		return fieldString(item, explicit)
	}
	for _, candidate := range defaults {
		if v := fieldString(item, candidate); v != "" {
			return v
		}
	}
	return ""
}

func parseDate(value, layout string) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	layouts := []string{layout}
	if layout == "" {
		layouts = dateLayouts
	}
	for _, candidate := range layouts {
		if candidate == "" {
			continue
		}
		if t, err := time.Parse(candidate, value); err == nil {
			return &t
		}
	}
	return nil
}

func articleFromJSONItem(item map[string]any, cfg ExtractConfig, baseURL string, index int) Article {
	guid := firstField(item, cfg.GUIDField, defaultGUIDFields)
	title := firstField(item, cfg.TitleField, defaultTitleFields)
	link := firstField(item, cfg.LinkField, defaultLinkFields)
	description := firstField(item, cfg.DescriptionField, defaultDescriptionFields)
	published := parseDate(firstField(item, cfg.PublishedField, defaultPublishedFields), cfg.DateLayout)

	var extra map[string]string
	if len(cfg.Extras) > 0 {
		extra = make(map[string]string, len(cfg.Extras))
		for name, spec := range cfg.Extras {
			// Keys are lowercase so template lookups match case-insensitively.
			extra[strings.ToLower(name)] = fieldString(item, spec)
		}
	}

	if guid == "" {
		// Stable fallback key: hash of the raw item JSON.
		raw, _ := json.Marshal(item)
		guid = HashKey(string(raw))
	}
	if title == "" {
		title = guid
	}
	if title == "" {
		title = fmt.Sprintf("Item %d", index+1)
	}
	if link != "" {
		link = resolveURL(baseURL, link)
	}

	summary := Truncate(StripHTML(description), 300)
	return Article{
		Key:         ArticleKey(guid, link, title, published),
		GUID:        textPtr(guid),
		Link:        textPtr(link),
		Title:       title,
		Description: description,
		Summary:     summary,
		PublishedAt: published,
		Extra:       extra,
	}
}

// resolveURL makes href absolute against the final request URL.
func resolveURL(base, href string) string {
	if href == "" {
		return ""
	}
	parsed, err := url.Parse(href)
	if err != nil || parsed.IsAbs() || base == "" {
		return href
	}
	baseParsed, err := url.Parse(base)
	if err != nil {
		return href
	}
	return baseParsed.ResolveReference(parsed).String()
}

// ---- HTML mode ----

func articlesFromHTML(body []byte, cfg ExtractConfig, baseURL string) ([]Article, error) {
	if cfg.ItemSelector == "" {
		return pageDiffArticle(body, baseURL)
	}

	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return nil, &FetchError{Message: "Failed to parse HTML: " + err.Error()}
	}
	items := doc.Find(cfg.ItemSelector)
	articles := make([]Article, 0, items.Length())
	items.Each(func(i int, sel *goquery.Selection) {
		title := extractSelection(sel, cfg.TitleSelector, baseURL)
		if title == "" {
			if linkText := extractSelection(sel, "a", baseURL); linkText != "" {
				title = linkText
			} else {
				title = Truncate(strings.TrimSpace(sel.Text()), 200)
			}
		}
		link := extractAttr(sel, cfg.LinkSelector, "a", "href", baseURL)
		description := extractSelection(sel, cfg.DescriptionSelector, baseURL)
		published := parseDate(extractSelection(sel, cfg.DateSelector, baseURL), cfg.DateLayout)

		title = strings.TrimSpace(StripHTML(title))
		description = strings.TrimSpace(StripHTML(description))
		articles = append(articles, Article{
			Key:         ArticleKey("", link, title, published),
			Link:        textPtr(link),
			Title:       title,
			Description: description,
			Summary:     Truncate(description, 300),
			PublishedAt: published,
		})
	})
	return articles, nil
}

// pageDiffArticle builds the single pseudo-article for whole-page diff mode:
// the key is the SHA-1 of the normalized page text, so each new content
// state becomes one fresh article.
func pageDiffArticle(body []byte, baseURL string) ([]Article, error) {
	content := StripHTML(string(body))
	if content == "" {
		return nil, &FetchError{Message: "Page appears empty (no extractable text)"}
	}
	return []Article{{
		Key:         HashKey("page:" + content),
		Link:        textPtr(baseURL),
		Title:       "Page updated",
		Description: content,
		Summary:     Truncate(content, 300),
	}}, nil
}

// extractSelection resolves a "selector" or "selector@attr" spec against a
// scope; an empty spec yields the scope's text.
func extractSelection(scope *goquery.Selection, spec, baseURL string) string {
	selector, attr := splitSpec(spec)
	if spec == "" {
		return ""
	}
	node := scope
	if selector != "" {
		node = scope.Find(selector).First()
	}
	if attr != "" {
		value := node.AttrOr(attr, "")
		// Only URL-carrying attributes are resolved against the base URL.
		if attr == "href" || attr == "src" {
			return resolveURL(baseURL, value)
		}
		return value
	}
	return strings.TrimSpace(node.Text())
}

// extractAttr is extractSelection with a fallback selector/attr pair when
// the spec is empty (HTML item links default to the first <a href>).
func extractAttr(scope *goquery.Selection, spec, fallbackSelector, fallbackAttr, baseURL string) string {
	if spec == "" {
		return resolveURL(baseURL, scope.Find(fallbackSelector).First().AttrOr(fallbackAttr, ""))
	}
	selector, attr := splitSpec(spec)
	if attr == "" {
		attr = fallbackAttr
	}
	if selector == "" {
		selector = fallbackSelector
	}
	return resolveURL(baseURL, scope.Find(selector).First().AttrOr(attr, ""))
}

func splitSpec(spec string) (selector, attr string) {
	if idx := strings.LastIndex(spec, "@"); idx > 0 {
		return strings.TrimSpace(spec[:idx]), strings.TrimSpace(spec[idx+1:])
	}
	return strings.TrimSpace(spec), ""
}

// ---- auto-detection helpers (bot-side probe) ----

var feedLinkRe = regexp.MustCompile(`(?is)<link[^>]+>`)

// DetectFeedLink returns the absolute href of the first RSS/Atom <link>
// declared by an HTML page, or nil. Used to suggest a real feed when adding
// a page that has one.
func DetectFeedLink(body []byte, baseURL string) *string {
	tags := feedLinkRe.FindAllString(string(body), -1)
	for _, tag := range tags {
		if !regexp.MustCompile(`(?i)type\s*=\s*["']application/(rss|atom)\+xml["']`).MatchString(tag) {
			continue
		}
		href := regexp.MustCompile(`(?i)href\s*=\s*["']([^"']+)["']`).FindStringSubmatch(tag)
		if href == nil {
			continue
		}
		link := resolveURL(baseURL, href[1])
		return &link
	}
	return nil
}

// SniffSourceType resolves "auto": "json" when the body parses to a usable
// item array, "html" otherwise.
func SniffSourceType(body []byte, cfg ExtractConfig) string {
	if looksLikeJSON(body) {
		var root any
		if err := json.Unmarshal(body, &root); err == nil {
			if _, err := locateItems(root, cfg.ItemPath); err == nil {
				return SourceJSON
			}
		}
	}
	return SourceHTML
}
