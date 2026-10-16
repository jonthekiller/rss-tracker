package sender

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

func TestSendsInOrderPerChannel(t *testing.T) {
	var mu sync.Mutex
	var sent []string
	done := make(chan struct{})
	q := New(Options{
		SendDelayMs: 1,
		Send: func(channelID string, message OutboundMessage) error {
			mu.Lock()
			sent = append(sent, channelID+":"+message.Content)
			finished := len(sent) == 4
			mu.Unlock()
			if finished {
				close(done)
			}
			return nil
		},
		Delay: func(time.Duration) {},
	})

	q.Send("c1", OutboundMessage{Content: "a"})
	q.Send("c1", OutboundMessage{Content: "b"})
	q.Send("c2", OutboundMessage{Content: "c"})
	q.Send("c1", OutboundMessage{Content: "d"})

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
	// Per-channel FIFO order is guaranteed; cross-channel interleaving is not.
	mu.Lock()
	entries := append([]string{}, sent...)
	mu.Unlock()
	var c1, c2 []string
	for _, entry := range entries {
		if strings.HasPrefix(entry, "c1:") {
			c1 = append(c1, entry)
		} else {
			c2 = append(c2, entry)
		}
	}
	if len(c1) != 3 || c1[0] != "c1:a" || c1[1] != "c1:b" || c1[2] != "c1:d" {
		t.Errorf("c1 order broken: %v", c1)
	}
	if len(c2) != 1 || c2[0] != "c2:c" {
		t.Errorf("c2 order broken: %v", c2)
	}
}

func TestRetryOnRateLimit(t *testing.T) {
	attempts := 0
	done := make(chan struct{})
	q := New(Options{
		SendDelayMs: 1,
		Send: func(channelID string, message OutboundMessage) error {
			attempts++
			if attempts < 3 {
				return &discordgo.RateLimitError{RateLimit: &discordgo.RateLimit{TooManyRequests: &discordgo.TooManyRequests{RetryAfter: 5 * time.Millisecond}}}
			}
			close(done)
			return nil
		},
		Delay: func(d time.Duration) {},
	})
	q.Send("c1", OutboundMessage{Content: "x"})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
	if attempts != 3 {
		t.Errorf("attempts = %d", attempts)
	}
}

func TestOnSendErrorAfterRetries(t *testing.T) {
	called := make(chan string, 1)
	q := New(Options{
		SendDelayMs: 1,
		Send: func(channelID string, message OutboundMessage) error {
			return errors.New("permanent")
		},
		Delay: func(time.Duration) {},
		OnSendError: func(channelID string, message OutboundMessage, err error) {
			called <- channelID
		},
	})
	q.Send("chan", OutboundMessage{Content: "x"})
	if got := <-called; got != "chan" {
		t.Errorf("onSendError channel = %s", got)
	}
}

func TestTransientRetry(t *testing.T) {
	attempts := 0
	done := make(chan struct{})
	q := New(Options{
		SendDelayMs: 1,
		Send: func(channelID string, message OutboundMessage) error {
			attempts++
			if attempts < 2 {
				return NewTransientError(502)
			}
			close(done)
			return nil
		},
		Delay: func(time.Duration) {},
	})
	q.Send("c", OutboundMessage{Content: "x"})
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

func TestDepth(t *testing.T) {
	block := make(chan struct{})
	q := New(Options{
		SendDelayMs: 1,
		Send: func(channelID string, message OutboundMessage) error {
			<-block
			return nil
		},
	})
	q.Send("c", OutboundMessage{Content: "1"})
	q.Send("c", OutboundMessage{Content: "2"})
	// Wait until the pump popped the first message (blocked in-flight): one pending.
	deadline := time.Now().Add(time.Second)
	for (q.Depth("c") != 1 || q.TotalDepth() != 2) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if q.Depth("c") != 1 {
		t.Errorf("depth = %d, want 1", q.Depth("c"))
	}
	if q.TotalDepth() != 2 {
		t.Errorf("totalDepth = %d, want 2 (1 in flight + 1 pending)", q.TotalDepth())
	}
	close(block)
}

func TestGetRetryAfterMs(t *testing.T) {
	err := &discordgo.RateLimitError{RateLimit: &discordgo.RateLimit{TooManyRequests: &discordgo.TooManyRequests{RetryAfter: 1200 * time.Millisecond}}}
	got := getRetryAfterMs(err)
	if got == nil || *got != 1300*time.Millisecond {
		t.Errorf("getRetryAfterMs = %v, want 1.3s", got)
	}
	if getRetryAfterMs(errors.New("x")) != nil {
		t.Error("plain errors have no retry-after")
	}
}
