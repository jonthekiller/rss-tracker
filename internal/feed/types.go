// Package feed fetches and normalizes RSS/Atom/JSON feeds and detects new
// articles. The article key algorithm matches the TypeScript implementation
// byte-for-byte so seen-article state is interchangeable.
package feed

import "time"

// Article is a normalized feed entry.
type Article struct {
	// Key is the stable unique key within a feed: guid || link || sha1(title+pubdate).
	Key   string
	GUID  *string
	Link  *string
	Title string
	// Description is the raw (HTML) content, if any.
	Description string
	// Summary is the plain-text truncated description (max 300 chars).
	Summary     string
	Author      *string
	PublishedAt *time.Time
	Image       *string
}

// FeedMeta carries feed-level metadata.
type FeedMeta struct {
	Title *string
}

// FetchStatus distinguishes a successful fetch from an HTTP 304.
type FetchStatus string

const (
	StatusOK          FetchStatus = "ok"
	StatusNotModified FetchStatus = "not_modified"
)

// FetchFeedResult is the outcome of a feed fetch.
type FetchFeedResult struct {
	Status       FetchStatus
	Articles     []Article
	Meta         FeedMeta
	ETag         *string
	LastModified *string
	// DetectedType is set by FetchWatch: the resolved source type
	// (json/html) when the request was auto-detected.
	DetectedType string
}

// FetchError signals a failed fetch; StatusCode is 0 when unknown.
type FetchError struct {
	Message    string
	StatusCode int
}

func (e *FetchError) Error() string { return e.Message }
