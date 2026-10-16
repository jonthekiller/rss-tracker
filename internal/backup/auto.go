package backup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/jonthekiller/rss-tracker/internal/store"
)

// AutoBackupOptions configures the daily auto-backup.
type AutoBackupOptions struct {
	Store *store.FeedStore
	// Directory receives the dated backup files.
	Directory string
	// Hour is the hour of day (nil = disabled).
	Hour *int
	// Keep is the number of backup files to keep (default 7).
	Keep int
	// Now is an injectable clock (tests).
	Now func() time.Time
}

// WriteAutoBackup writes a dated backup file to the backup directory, keeping
// the most recent ones. Returns the written path ("" when disabled).
func WriteAutoBackup(options AutoBackupOptions) (string, error) {
	if options.Hour == nil {
		return "", nil
	}
	backup, err := ExportBackup(options.Store, nil)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(options.Directory, 0o755); err != nil {
		return "", err
	}
	filename := "rss-backup-" + isoDate(nowUTC()) + ".json"
	filePath := filepath.Join(options.Directory, filename)
	encoded, err := json.MarshalIndent(backup, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filePath, encoded, 0o644); err != nil {
		return "", err
	}
	if err := rotate(options.Directory, keepCount(options.Keep)); err != nil {
		return "", err
	}
	return filePath, nil
}

// StartAutoBackupLoop checks every 10 minutes whether the configured hour has
// been reached and today's backup has not been written yet. Returns a stop
// function.
func StartAutoBackupLoop(options AutoBackupOptions) func() {
	lastWrittenDate := ""
	// Apply retention immediately at startup, so lowering the keep count (or
	// deleting the backup setting) cleans up stale files without waiting a day.
	if options.Hour != nil {
		if _, err := os.Stat(options.Directory); err == nil {
			_ = rotate(options.Directory, keepCount(options.Keep))
		}
	}
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if options.Hour == nil {
					continue
				}
				now := nowUTC()
				today := isoDate(now)
				if now.Hour() < *options.Hour {
					continue
				}
				if lastWrittenDate == today {
					continue
				}
				filePath, err := WriteAutoBackup(options)
				if err == nil && filePath != "" {
					lastWrittenDate = today
				}
			}
		}
	}()
	return func() { close(done) }
}

func keepCount(keep int) int {
	if keep <= 0 {
		return 7
	}
	return keep
}

func isoDate(t time.Time) string {
	return t.UTC().Format("2006-01-02")
}

func rotate(directory string, keep int) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	var files []string
	for _, entry := range entries {
		name := entry.Name()
		if len(name) > len("rss-backup-") &&
			name[:len("rss-backup-")] == "rss-backup-" &&
			filepath.Ext(name) == ".json" {
			files = append(files, name)
		}
	}
	sort.Strings(files)
	for i := 0; i < len(files)-keep; i++ {
		if err := os.Remove(filepath.Join(directory, files[i])); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
