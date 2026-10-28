package feed

import (
	"testing"
	"time"

	"github.com/jonthekiller/rss-tracker/internal/store"
)

func article(title string, published *time.Time) Article {
	a := Article{Key: title, Title: title, Summary: "summary", Description: "desc"}
	a.PublishedAt = published
	return a
}

func TestDetectOldestFirst(t *testing.T) {
	now := int64(1_700_000_000_000)
	old := time.UnixMilli(now - 3600_000)
	new := time.UnixMilli(now - 60_000)
	result := DetectNewArticles(DetectionInput{
		Articles:  []Article{article("newest", &new), article("oldest", &old)},
		KnownKeys: map[string]bool{},
		Now:       now,
	})
	if len(result.Fresh) != 2 || result.Fresh[0].Title != "oldest" {
		t.Errorf("fresh order = %v", titles(result.Fresh))
	}
}

func TestDetectMaxAge(t *testing.T) {
	now := int64(1_700_000_000_000)
	old := time.UnixMilli(now - 40*86_400_000)
	recent := time.UnixMilli(now - 60_000)
	result := DetectNewArticles(DetectionInput{
		Articles:   []Article{article("old", &old), article("recent", &recent)},
		KnownKeys:  map[string]bool{},
		MaxAgeDays: 30,
		Now:        now,
	})
	if len(result.TooOld) != 1 || result.TooOld[0].Title != "old" {
		t.Errorf("tooOld = %v", titles(result.TooOld))
	}
	if len(result.Fresh) != 1 {
		t.Errorf("fresh = %v", titles(result.Fresh))
	}
}

func TestDetectNoAgeCheckWhenDisabled(t *testing.T) {
	now := int64(1_700_000_000_000)
	ancient := time.UnixMilli(now - 400*86_400_000)
	result := DetectNewArticles(DetectionInput{
		Articles:   []Article{article("ancient", &ancient)},
		KnownKeys:  map[string]bool{},
		MaxAgeDays: 0,
		Now:        now,
	})
	if len(result.Fresh) != 1 {
		t.Errorf("maxAgeDays=0 must not filter, got %v", titles(result.Fresh))
	}
}

func TestIncludeFilters(t *testing.T) {
	now := int64(1_700_000_000_000)
	a := article("golang news", nil)
	result := DetectNewArticles(DetectionInput{
		Articles:  []Article{a, article("other", nil)},
		KnownKeys: map[string]bool{},
		Now:       now,
		Filters:   []*store.FeedFilter{{Kind: store.FilterInclude, Field: store.FieldTitle, Pattern: "golang"}},
	})
	if len(result.Fresh) != 1 || result.Fresh[0].Title != "golang news" {
		t.Errorf("fresh = %v", titles(result.Fresh))
	}
	if len(result.Blocked) != 1 {
		t.Errorf("blocked = %v", titles(result.Blocked))
	}
}

func TestExcludeWinsOverInclude(t *testing.T) {
	now := int64(1_700_000_000_000)
	result := DetectNewArticles(DetectionInput{
		Articles:  []Article{article("golang spam", nil)},
		KnownKeys: map[string]bool{},
		Now:       now,
		Filters: []*store.FeedFilter{
			{Kind: store.FilterInclude, Field: store.FieldTitle, Pattern: "golang"},
			{Kind: store.FilterExclude, Field: store.FieldTitle, Pattern: "spam"},
		},
	})
	if len(result.Fresh) != 0 || len(result.Blocked) != 1 {
		t.Errorf("exclude must win: fresh=%v blocked=%v", titles(result.Fresh), titles(result.Blocked))
	}
}

func TestRegexFilter(t *testing.T) {
	now := int64(1_700_000_000_000)
	result := DetectNewArticles(DetectionInput{
		Articles:  []Article{article("Release 2.0", nil)},
		KnownKeys: map[string]bool{},
		Now:       now,
		Filters:   []*store.FeedFilter{{Kind: store.FilterInclude, Field: store.FieldTitle, Pattern: `release \d`, IsRegex: true}},
	})
	if len(result.Fresh) != 1 {
		t.Errorf("regex filter should match: %v", titles(result.Fresh))
	}
}

func TestDescriptionFieldMatch(t *testing.T) {
	now := int64(1_700_000_000_000)
	a := article("neutral", nil)
	a.Description = "contains the word needle inside"
	result := DetectNewArticles(DetectionInput{
		Articles:  []Article{a},
		KnownKeys: map[string]bool{},
		Now:       now,
		Filters:   []*store.FeedFilter{{Kind: store.FilterInclude, Field: store.FieldAny, Pattern: "needle"}},
	})
	if len(result.Fresh) != 1 {
		t.Errorf("field any must match description: %v", titles(result.Fresh))
	}
}

func titles(articles []Article) []string {
	out := []string{}
	for _, a := range articles {
		out = append(out, a.Title)
	}
	return out
}
