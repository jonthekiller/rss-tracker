package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type BatchMode string

const (
	BatchAuto       BatchMode = "auto"
	BatchIndividual BatchMode = "individual"
	BatchDigest     BatchMode = "digest"
)

type FilterKind string

const (
	FilterInclude FilterKind = "include"
	FilterExclude FilterKind = "exclude"
)

type FilterField string

const (
	FieldTitle       FilterField = "title"
	FieldDescription FilterField = "description"
	FieldAny         FilterField = "any"
)

// Source types for feeds.source_type (v3 migration).
const (
	SourceTypeRSS  = "rss"
	SourceTypeJSON = "json"
	SourceTypeHTML = "html"
	SourceTypeAuto = "auto"
)

// Feed mirrors the feeds table row.
type Feed struct {
	ID                  string
	Name                string
	URL                 string
	GuildID             string
	ChannelID           string
	Enabled             bool
	IntervalMinutes     int
	NextCheckAt         int64
	TemplateText        string
	EmbedJSON           *string
	BatchMode           BatchMode
	BatchThreshold      int
	BatchMaxItems       int
	MaxAgeDays          int
	SourceType          string // rss | json | html | auto
	ExtractConfig       *string
	ETag                *string
	LastModified        *string
	LastCheckedAt       *int64
	LastError           *string
	ConsecutiveFailures int
	FailedNotified      bool
	PauseReason         *string
	CreatedAt           int64
}

// FeedFilter mirrors the filters table row.
type FeedFilter struct {
	ID      int64
	FeedID  string
	Kind    FilterKind
	Field   FilterField
	Pattern string
	IsRegex bool
}

// NewFeed carries the fields set on insertion (defaults applied in AddFeed).
// Enabled is nil when unset (feeds are enabled by default).
type NewFeed struct {
	ID              string
	Name            string
	URL             string
	GuildID         string
	ChannelID       string
	TemplateText    string
	EmbedJSON       *string
	IntervalMinutes int
	BatchMode       BatchMode
	BatchThreshold  int
	BatchMaxItems   int
	MaxAgeDays      int
	SourceType      string
	ExtractConfig   *string
	Enabled         *bool
	PauseReason     *string
	HasInterval     bool
	HasBatchMode    bool
	HasThreshold    bool
	HasMaxItems     bool
	HasMaxAgeDays   bool
}

// FeedPatch carries an update; nil fields are left unchanged.
type FeedPatch struct {
	Name                *string
	ChannelID           *string
	Enabled             *bool
	IntervalMinutes     *int
	NextCheckAt         *int64
	TemplateText        *string
	EmbedJSON           **string
	BatchMode           *BatchMode
	BatchThreshold      *int
	BatchMaxItems       *int
	MaxAgeDays          *int
	SourceType          *string
	ExtractConfig       **string
	ETag                **string
	LastModified        **string
	LastCheckedAt       **int64
	LastError           **string
	ConsecutiveFailures *int
	FailedNotified      *bool
	// PauseReason sets a new pause reason; ClearPauseReason removes it.
	PauseReason      *string
	ClearPauseReason bool
}

const feedColumns = `id, name, url, guild_id, channel_id, enabled, interval_minutes, next_check_at,
  template_text, embed_json, batch_mode, batch_threshold, batch_max_items, max_age_days,
  source_type, extract_config, etag, last_modified, last_checked_at, last_error,
  consecutive_failures, failed_notified, pause_reason, created_at`

// FeedStore provides CRUD over the feeds/filters/articles_seen/settings tables.
type FeedStore struct {
	db *sql.DB
}

func NewFeedStore(db *sql.DB) *FeedStore {
	return &FeedStore{db: db}
}

func (s *FeedStore) Close() error { return s.db.Close() }

func scanFeed(row interface{ Scan(...any) error }) (*Feed, error) {
	var f Feed
	var enabled, failedNotified int
	var batchMode string
	err := row.Scan(
		&f.ID, &f.Name, &f.URL, &f.GuildID, &f.ChannelID, &enabled, &f.IntervalMinutes,
		&f.NextCheckAt, &f.TemplateText, &f.EmbedJSON, &batchMode, &f.BatchThreshold,
		&f.BatchMaxItems, &f.MaxAgeDays, &f.SourceType, &f.ExtractConfig, &f.ETag,
		&f.LastModified, &f.LastCheckedAt, &f.LastError, &f.ConsecutiveFailures,
		&failedNotified, &f.PauseReason, &f.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	f.Enabled = enabled == 1
	f.FailedNotified = failedNotified == 1
	f.BatchMode = BatchMode(batchMode)
	if f.SourceType == "" {
		f.SourceType = "rss"
	}
	return &f, nil
}

// GetFeed returns nil when the feed does not exist.
func (s *FeedStore) GetFeed(id string) (*Feed, error) {
	feed, err := scanFeed(s.db.QueryRow(`SELECT `+feedColumns+` FROM feeds WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return feed, err
}

func (s *FeedStore) getFeeds(query string, args ...any) ([]*Feed, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	feeds := []*Feed{}
	for rows.Next() {
		feed, err := scanFeed(rows)
		if err != nil {
			return nil, err
		}
		feeds = append(feeds, feed)
	}
	return feeds, rows.Err()
}

func (s *FeedStore) GetFeedsForGuild(guildID string) ([]*Feed, error) {
	return s.getFeeds(`SELECT `+feedColumns+` FROM feeds WHERE guild_id = ? ORDER BY name, url`, guildID)
}

func (s *FeedStore) GetFeedsForChannel(channelID string) ([]*Feed, error) {
	return s.getFeeds(`SELECT `+feedColumns+` FROM feeds WHERE channel_id = ? ORDER BY name, url`, channelID)
}

func (s *FeedStore) GetAllFeeds() ([]*Feed, error) {
	return s.getFeeds(`SELECT ` + feedColumns + ` FROM feeds ORDER BY name, url`)
}

func (s *FeedStore) GetFeedsDueNow(now int64, limit int) ([]*Feed, error) {
	return s.getFeeds(
		`SELECT `+feedColumns+` FROM feeds WHERE enabled = 1 AND next_check_at <= ? ORDER BY next_check_at LIMIT ?`,
		now, limit,
	)
}

func (s *FeedStore) Exists(url, channelID string) (bool, error) {
	var one any
	err := s.db.QueryRow(`SELECT 1 FROM feeds WHERE url = ? AND channel_id = ?`, url, channelID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// AddFeed inserts a feed with defaults matching the TS implementation.
func (s *FeedStore) AddFeed(feed NewFeed) (*Feed, error) {
	interval := 10
	if feed.HasInterval {
		interval = feed.IntervalMinutes
	}
	batchMode := BatchAuto
	if feed.HasBatchMode {
		batchMode = feed.BatchMode
	}
	batchThreshold := 5
	if feed.HasThreshold {
		batchThreshold = feed.BatchThreshold
	}
	batchMaxItems := 15
	if feed.HasMaxItems {
		batchMaxItems = feed.BatchMaxItems
	}
	maxAgeDays := 30
	if feed.HasMaxAgeDays {
		maxAgeDays = feed.MaxAgeDays
	}
	name := feed.Name
	if name == "" {
		name = "Unknown"
	}
	template := feed.TemplateText
	if template == "" {
		template = "**{title}**\n{link}"
	}
	enabled := 1
	if feed.Enabled != nil && !*feed.Enabled {
		enabled = 0
	}
	sourceType := feed.SourceType
	if sourceType == "" {
		sourceType = "rss"
	}
	_, err := s.db.Exec(
		`INSERT INTO feeds (id, name, url, guild_id, channel_id, enabled, interval_minutes, next_check_at,
      template_text, embed_json, batch_mode, batch_threshold, batch_max_items, max_age_days,
      source_type, extract_config, pause_reason)
     VALUES (?, ?, ?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		feed.ID, name, feed.URL, feed.GuildID, feed.ChannelID, enabled, interval,
		template, feed.EmbedJSON, string(batchMode), batchThreshold, batchMaxItems, maxAgeDays,
		sourceType, feed.ExtractConfig, feed.PauseReason,
	)
	if err != nil {
		return nil, err
	}
	return s.GetFeed(feed.ID)
}

func (s *FeedStore) UpdateFeed(id string, patch FeedPatch) error {
	var sets []string
	var values []any
	if patch.Name != nil {
		sets = append(sets, "name = ?")
		values = append(values, *patch.Name)
	}
	if patch.ChannelID != nil {
		sets = append(sets, "channel_id = ?")
		values = append(values, *patch.ChannelID)
	}
	if patch.Enabled != nil {
		sets = append(sets, "enabled = ?")
		values = append(values, boolToInt(*patch.Enabled))
	}
	if patch.IntervalMinutes != nil {
		sets = append(sets, "interval_minutes = ?")
		values = append(values, *patch.IntervalMinutes)
	}
	if patch.NextCheckAt != nil {
		sets = append(sets, "next_check_at = ?")
		values = append(values, *patch.NextCheckAt)
	}
	if patch.TemplateText != nil {
		sets = append(sets, "template_text = ?")
		values = append(values, *patch.TemplateText)
	}
	if patch.EmbedJSON != nil {
		sets = append(sets, "embed_json = ?")
		values = append(values, *patch.EmbedJSON)
	}
	if patch.BatchMode != nil {
		sets = append(sets, "batch_mode = ?")
		values = append(values, string(*patch.BatchMode))
	}
	if patch.BatchThreshold != nil {
		sets = append(sets, "batch_threshold = ?")
		values = append(values, *patch.BatchThreshold)
	}
	if patch.BatchMaxItems != nil {
		sets = append(sets, "batch_max_items = ?")
		values = append(values, *patch.BatchMaxItems)
	}
	if patch.MaxAgeDays != nil {
		sets = append(sets, "max_age_days = ?")
		values = append(values, *patch.MaxAgeDays)
	}
	if patch.SourceType != nil {
		sets = append(sets, "source_type = ?")
		values = append(values, *patch.SourceType)
	}
	if patch.ExtractConfig != nil {
		sets = append(sets, "extract_config = ?")
		values = append(values, *patch.ExtractConfig)
	}
	if patch.ETag != nil {
		sets = append(sets, "etag = ?")
		values = append(values, *patch.ETag)
	}
	if patch.LastModified != nil {
		sets = append(sets, "last_modified = ?")
		values = append(values, *patch.LastModified)
	}
	if patch.LastCheckedAt != nil {
		sets = append(sets, "last_checked_at = ?")
		values = append(values, *patch.LastCheckedAt)
	}
	if patch.LastError != nil {
		sets = append(sets, "last_error = ?")
		values = append(values, *patch.LastError)
	}
	if patch.ConsecutiveFailures != nil {
		sets = append(sets, "consecutive_failures = ?")
		values = append(values, *patch.ConsecutiveFailures)
	}
	if patch.FailedNotified != nil {
		sets = append(sets, "failed_notified = ?")
		values = append(values, boolToInt(*patch.FailedNotified))
	}
	if patch.PauseReason != nil {
		sets = append(sets, "pause_reason = ?")
		values = append(values, *patch.PauseReason)
	} else if patch.ClearPauseReason {
		sets = append(sets, "pause_reason = NULL")
	}
	if len(sets) == 0 {
		return nil
	}
	values = append(values, id)
	_, err := s.db.Exec(`UPDATE feeds SET `+strings.Join(sets, ", ")+` WHERE id = ?`, values...)
	return err
}

func (s *FeedStore) DeleteFeed(id string) error {
	_, err := s.db.Exec(`DELETE FROM feeds WHERE id = ?`, id)
	return err
}

func (s *FeedStore) SetNextCheck(id string, nextCheckAt int64) error {
	_, err := s.db.Exec(`UPDATE feeds SET next_check_at = ? WHERE id = ?`, nextCheckAt, id)
	return err
}

func (s *FeedStore) MarkChecked(id string, now int64, etag, lastModified *string) error {
	_, err := s.db.Exec(
		`UPDATE feeds SET last_checked_at = ?, etag = COALESCE(?, etag), last_modified = COALESCE(?, last_modified), last_error = NULL, consecutive_failures = 0, failed_notified = 0 WHERE id = ?`,
		now, etag, lastModified, id,
	)
	return err
}

func (s *FeedStore) MarkFailure(id string, now int64, errMsg string) error {
	if len(errMsg) > 500 {
		errMsg = errMsg[:500]
	}
	_, err := s.db.Exec(
		`UPDATE feeds SET last_checked_at = ?, last_error = ?, consecutive_failures = consecutive_failures + 1 WHERE id = ?`,
		now, errMsg, id,
	)
	return err
}

// ---- filters ----

func (s *FeedStore) GetFilters(feedID string) ([]*FeedFilter, error) {
	rows, err := s.db.Query(`SELECT id, feed_id, kind, field, pattern, is_regex FROM filters WHERE feed_id = ? ORDER BY id`, feedID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	filters := []*FeedFilter{}
	for rows.Next() {
		var f FeedFilter
		var kind, field string
		var isRegex int
		if err := rows.Scan(&f.ID, &f.FeedID, &kind, &field, &f.Pattern, &isRegex); err != nil {
			return nil, err
		}
		f.Kind = FilterKind(kind)
		f.Field = FilterField(field)
		f.IsRegex = isRegex == 1
		filters = append(filters, &f)
	}
	return filters, rows.Err()
}

func (s *FeedStore) AddFilter(feedID string, kind FilterKind, field FilterField, pattern string, isRegex bool) error {
	_, err := s.db.Exec(
		`INSERT INTO filters (feed_id, kind, field, pattern, is_regex) VALUES (?, ?, ?, ?, ?)`,
		feedID, string(kind), string(field), pattern, boolToInt(isRegex),
	)
	return err
}

func (s *FeedStore) RemoveFilter(feedID string, filterID int64) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM filters WHERE feed_id = ? AND id = ?`, feedID, filterID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (s *FeedStore) RemoveAllFilters(feedID string) error {
	_, err := s.db.Exec(`DELETE FROM filters WHERE feed_id = ?`, feedID)
	return err
}

// ---- seen articles ----

func (s *FeedStore) GetSeenKeys(feedID string) (map[string]bool, error) {
	rows, err := s.db.Query(`SELECT article_key FROM articles_seen WHERE feed_id = ?`, feedID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := map[string]bool{}
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys[key] = true
	}
	return keys, rows.Err()
}

// MarkSeen inserts keys with their published timestamps (milliseconds, nil if
// unknown) inside one transaction, ignoring duplicates.
func (s *FeedStore) MarkSeen(feedID string, keys []string, publishedAt []*int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO articles_seen (feed_id, article_key, published_at) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for i, key := range keys {
		var published any
		if i < len(publishedAt) && publishedAt[i] != nil {
			published = *publishedAt[i]
		}
		if _, err := stmt.Exec(feedID, key, published); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *FeedStore) CountSeen(feedID string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM articles_seen WHERE feed_id = ?`, feedID).Scan(&n)
	return n, err
}

// ---- settings ----

func (s *FeedStore) GetSetting(key string) (*string, error) {
	var value sql.NullString
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !value.Valid {
		return nil, nil
	}
	v := value.String
	return &v, nil
}

func (s *FeedStore) SetSetting(key string, value *string) error {
	_, err := s.db.Exec(
		`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		key, value,
	)
	return err
}

func (s *FeedStore) GetAllSettings() (map[string]*string, error) {
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	settings := map[string]*string{}
	for rows.Next() {
		var key string
		var value sql.NullString
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		if value.Valid {
			v := value.String
			settings[key] = &v
		} else {
			settings[key] = nil
		}
	}
	return settings, rows.Err()
}

// ---- per-channel settings (v2 model) ----

func (s *FeedStore) GetChannelSetting(channelID, key string) (*string, error) {
	var value sql.NullString
	err := s.db.QueryRow(
		`SELECT value FROM channel_settings WHERE channel_id = ? AND key = ?`, channelID, key,
	).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !value.Valid {
		return nil, nil
	}
	v := value.String
	return &v, nil
}

func (s *FeedStore) SetChannelSetting(channelID, key string, value *string) error {
	if value == nil {
		_, err := s.db.Exec(`DELETE FROM channel_settings WHERE channel_id = ? AND key = ?`, channelID, key)
		return err
	}
	_, err := s.db.Exec(
		`INSERT INTO channel_settings (channel_id, key, value) VALUES (?, ?, ?) ON CONFLICT (channel_id, key) DO UPDATE SET value = excluded.value`,
		channelID, key, *value,
	)
	return err
}

func (s *FeedStore) GetAllChannelSettings(channelID string) (map[string]*string, error) {
	rows, err := s.db.Query(`SELECT key, value FROM channel_settings WHERE channel_id = ?`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	settings := map[string]*string{}
	for rows.Next() {
		var key string
		var value sql.NullString
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		if value.Valid {
			v := value.String
			settings[key] = &v
		} else {
			settings[key] = nil
		}
	}
	return settings, rows.Err()
}

// GetAllChannelIDs returns all channel ids having feeds or settings.
func (s *FeedStore) GetAllChannelIDs() ([]string, error) {
	rows, err := s.db.Query(
		`SELECT DISTINCT channel_id FROM feeds UNION SELECT DISTINCT channel_id FROM channel_settings`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// NowMillis is the timestamp convention used across the schema (ms epoch).
func NowMillis() int64 { return time.Now().UnixMilli() }

var _ = fmt.Sprintf // keep fmt import for future use
