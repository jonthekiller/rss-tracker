package feed

import (
	"regexp"
	"sort"
	"strings"

	"github.com/jonthekiller/rss-tracker/internal/store"
)

// DetectionInput mirrors the TS DetectionInput.
type DetectionInput struct {
	// Articles are in feed order (newest first usually).
	Articles []Article
	// KnownKeys are already-persisted + current-cycle cached keys.
	KnownKeys map[string]bool
	// MaxAgeDays: 0 disables the age guard.
	MaxAgeDays int
	Now        int64 // milliseconds
	Filters    []*store.FeedFilter
}

// DetectionResult holds the triaged articles.
type DetectionResult struct {
	// Fresh are new articles that passed filters and age checks, oldest first.
	Fresh []Article
	// Blocked are articles rejected by include/exclude filters.
	Blocked []Article
	// TooOld are articles ignored because of the age guard.
	TooOld []Article
}

const millisPerDay = 86_400_000

// DetectNewArticles determines which articles are new. Fresh articles are
// sorted oldest-first so delivery matches publication order.
func DetectNewArticles(input DetectionInput) DetectionResult {
	var maxAgeMs int64
	if input.MaxAgeDays > 0 {
		maxAgeMs = int64(input.MaxAgeDays) * millisPerDay
	}
	result := DetectionResult{Fresh: []Article{}, Blocked: []Article{}, TooOld: []Article{}}

	for _, article := range input.Articles {
		if input.KnownKeys[article.Key] {
			continue
		}
		if maxAgeMs > 0 && article.PublishedAt != nil &&
			input.Now-article.PublishedAt.UnixMilli() > maxAgeMs {
			result.TooOld = append(result.TooOld, article)
			continue
		}
		if !PassesFilters(article, input.Filters) {
			result.Blocked = append(result.Blocked, article)
			continue
		}
		result.Fresh = append(result.Fresh, article)
	}

	// Oldest first so messages are posted in publication order.
	sort.SliceStable(result.Fresh, func(i, j int) bool {
		ti, tj := int64(0), int64(0)
		if result.Fresh[i].PublishedAt != nil {
			ti = result.Fresh[i].PublishedAt.UnixMilli()
		}
		if result.Fresh[j].PublishedAt != nil {
			tj = result.Fresh[j].PublishedAt.UnixMilli()
		}
		return ti < tj
	})
	return result
}

// PassesFilters applies exclude-then-include semantics: any matching exclude
// rejects the article; when include filters exist, at least one must match.
func PassesFilters(article Article, filters []*store.FeedFilter) bool {
	var includes, excludes []*store.FeedFilter
	for _, f := range filters {
		switch f.Kind {
		case store.FilterInclude:
			includes = append(includes, f)
		case store.FilterExclude:
			excludes = append(excludes, f)
		}
	}

	for _, f := range excludes {
		if matchFilter(article, f) {
			return false
		}
	}

	if len(includes) > 0 {
		for _, f := range includes {
			if matchFilter(article, f) {
				return true
			}
		}
		return false
	}
	return true
}

// filterFields returns the haystacks to search for a given filter field.
func filterFields(article Article, field store.FilterField) []string {
	var fields []string
	if field == store.FieldTitle || field == store.FieldAny {
		fields = append(fields, article.Title)
	}
	if field == store.FieldDescription || field == store.FieldAny {
		fields = append(fields, article.Summary, article.Description)
	}
	return fields
}

func matchFilter(article Article, filter *store.FeedFilter) bool {
	fields := filterFields(article, filter.Field)

	if filter.IsRegex {
		// RE2 note: JS-only constructs (lookahead, backreferences) fail to
		// compile and therefore never match — same net effect as the TS
		// version, which treated invalid regexes as non-matching.
		re, err := regexp.Compile(`(?i)` + filter.Pattern)
		if err != nil {
			return false
		}
		for _, field := range fields {
			if re.MatchString(field) {
				return true
			}
		}
		return false
	}
	needle := strings.ToLower(filter.Pattern)
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field), needle) {
			return true
		}
	}
	return false
}
