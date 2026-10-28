package render

import (
	"fmt"
	"regexp"
	"strings"
)

// DigestItem is one entry of a digest message.
type DigestItem struct {
	Title string
	Link  *string
}

// DigestOptions carries the feed name for the header (timezone is unused for
// digests today but kept for template parity).
type DigestOptions struct {
	FeedName string
	Timezone string
}

var newlineRunRe = regexp.MustCompile(`\n+`)

// runeLen mirrors the TS string .length (count of characters, not bytes).
func runeLen(s string) int { return len([]rune(s)) }

// truncateRunes cuts a string to n runes without splitting multi-byte chars.
func truncateRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

// BuildDigestMessages builds the digest message(s) for a burst of articles.
// Every item is listed; when the recap does not fit in a single message it is
// split into as many messages as needed (max 2000 chars each), header only on
// the first one.
func BuildDigestMessages(items []DigestItem, options DigestOptions) []Payload {
	if len(items) == 0 {
		return []Payload{}
	}

	lines := make([]string, 0, len(items))
	for i, item := range items {
		number := fmt.Sprintf("%d.", i+1)
		title := strings.TrimSpace(newlineRunRe.ReplaceAllString(item.Title, " "))
		if title == "" {
			title = "untitled"
		}
		if item.Link != nil {
			lines = append(lines, fmt.Sprintf("%s [%s](<%s>)", number, title, *item.Link))
		} else {
			lines = append(lines, fmt.Sprintf("%s %s", number, title))
		}
	}

	total := len(items)
	suffix := ""
	if total > 1 {
		suffix = "s"
	}
	header := fmt.Sprintf("📰 **%s** — %d new article%s:", options.FeedName, total, suffix)
	fullBody := strings.Join(lines, "\n")

	if runeLen(header)+1+runeLen(fullBody) <= LimitContent {
		return []Payload{{Content: header + "\n" + fullBody}}
	}

	// Split across multiple messages: header only on the first one.
	messages := []Payload{}
	var currentLines []string
	currentLength := 0

	pushCurrent := func() {
		if len(currentLines) == 0 {
			return
		}
		if len(messages) == 0 {
			messages = append(messages, Payload{Content: header + "\n" + strings.Join(currentLines, "\n")})
		} else {
			messages = append(messages, Payload{Content: strings.Join(currentLines, "\n")})
		}
		currentLines = []string{}
		currentLength = 0
	}

	for _, line := range lines {
		lineLength := runeLen(line) + 1
		// The first message carries the header, so its body budget is smaller.
		budget := LimitContent
		if len(messages) == 0 {
			budget = LimitContent - runeLen(header) - 1
		}
		if currentLength+lineLength > budget && len(currentLines) > 0 {
			pushCurrent()
		}
		// A single line longer than a whole message gets truncated.
		if runeLen(line) > LimitContent {
			currentLines = append(currentLines, truncateRunes(line, LimitContent-3)+"...")
			currentLength += LimitContent
			continue
		}
		currentLines = append(currentLines, line)
		currentLength += lineLength
	}
	pushCurrent()

	return messages
}
