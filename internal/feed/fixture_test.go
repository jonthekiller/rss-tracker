package feed

import (
	"os"
	"testing"
)

// TestFixtureRSSParses keeps the captured RSS fixture parseable.
func TestFixtureRSSParses(t *testing.T) {
	data, err := os.ReadFile("testdata/rss.xml")
	if err != nil {
		t.Skip("fixture missing")
	}
	parser := newParser()
	parsed, err := parser.ParseString(string(data))
	if err != nil {
		t.Fatalf("fixture no longer parseable: %v", err)
	}
	if len(parsed.Items) == 0 {
		t.Error("fixture has no items")
	}
}
