// Package sender implements the per-channel FIFO delivery queue with pacing
// and rate-limit awareness, matching the TypeScript SenderQueue semantics:
// order preserved, messages spaced by SEND_DELAY_MS, 429 retry_after honored,
// and no message loss on shutdown for messages already enqueued (seen keys are
// marked at enqueue time, so a crash can lose pending messages but never
// duplicates).
package sender

import (
	"errors"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/jonthekiller/rss-tracker/internal/render"
)

// OutboundMessage is an outgoing Discord message.
type OutboundMessage = render.Payload

// SendFn delivers a message to a channel (bot client in prod, stub in tests).
type SendFn func(channelID string, message OutboundMessage) error

// Options configures a SenderQueue.
type Options struct {
	// SendDelayMs is the minimum delay between two messages to the same channel.
	SendDelayMs int
	// Send is the injected send function.
	Send SendFn
	// Delay is an injectable timer (tests); defaults to time.Sleep.
	Delay func(time.Duration)
	// OnSendError is invoked when a message is definitively dropped.
	OnSendError func(channelID string, message OutboundMessage, err error)
}

type channelQueue struct {
	messages []OutboundMessage
	running  bool
}

// SenderQueue is the per-channel FIFO sender.
type SenderQueue struct {
	options Options
	mu      sync.Mutex
	queues  map[string]*channelQueue
}

// New creates a SenderQueue.
func New(options Options) *SenderQueue {
	if options.Delay == nil {
		options.Delay = time.Sleep
	}
	return &SenderQueue{options: options, queues: map[string]*channelQueue{}}
}

// Depth returns the pending message count for a channel (0 if unknown).
func (s *SenderQueue) Depth(channelID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if q, ok := s.queues[channelID]; ok {
		return len(q.messages)
	}
	return 0
}

// TotalDepth returns the pending message count across all channels.
func (s *SenderQueue) TotalDepth() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	total := 0
	for _, q := range s.queues {
		total += len(q.messages)
		if q.running {
			total++
		}
	}
	return total
}

// Send enqueues a message; delivery happens in the background.
func (s *SenderQueue) Send(channelID string, message OutboundMessage) {
	s.mu.Lock()
	q, ok := s.queues[channelID]
	if !ok {
		q = &channelQueue{}
		s.queues[channelID] = q
	}
	q.messages = append(q.messages, message)
	shouldStart := !q.running
	if shouldStart {
		q.running = true
	}
	s.mu.Unlock()
	if shouldStart {
		go s.pump(channelID, q)
	}
}

func (s *SenderQueue) pump(channelID string, q *channelQueue) {
	for {
		for {
			s.mu.Lock()
			if len(q.messages) == 0 {
				s.mu.Unlock()
				break
			}
			message := q.messages[0]
			q.messages = q.messages[1:]
			s.mu.Unlock()

			s.deliver(channelID, message)

			s.mu.Lock()
			more := len(q.messages) > 0
			s.mu.Unlock()
			if more {
				s.options.Delay(time.Duration(s.options.SendDelayMs) * time.Millisecond)
			}
		}

		s.mu.Lock()
		restart := len(q.messages) > 0
		if !restart {
			q.running = false
		}
		s.mu.Unlock()
		if !restart {
			return
		}
	}
}

func (s *SenderQueue) deliver(channelID string, message OutboundMessage) {
	attempt := 0
	for {
		err := s.options.Send(channelID, message)
		if err == nil {
			return
		}
		if retryAfterMs := getRetryAfterMs(err); retryAfterMs != nil && attempt < 5 {
			attempt++
			s.options.Delay(*retryAfterMs)
			continue
		}
		if attempt < 2 && isTransientError(err) {
			attempt++
			s.options.Delay(time.Duration(1000*attempt) * time.Millisecond)
			continue
		}
		if s.options.OnSendError != nil {
			s.options.OnSendError(channelID, message, err)
		}
		return
	}
}

// GetRetryAfterMs extracts retry_after (in ms, +100 buffer) from a discord.js
// rate limit error, if any. Mirrors the TS getRetryAfterMs.
func getRetryAfterMs(err error) *time.Duration {
	var rateLimited *discordgo.RateLimitError
	if errors.As(err, &rateLimited) && rateLimited.RateLimit != nil && rateLimited.RateLimit.RetryAfter >= 0 {
		ms := time.Duration(math.Ceil(rateLimited.RateLimit.RetryAfter.Seconds()*1000))*time.Millisecond + 100*time.Millisecond
		return &ms
	}
	return nil
}

func isTransientError(err error) bool {
	var restErr *discordgo.RESTError
	if errors.As(err, &restErr) && restErr.Response != nil {
		return restErr.Response.StatusCode >= 500
	}
	var httpErr *httpError
	if errors.As(err, &httpErr) {
		return httpErr.status >= 500
	}
	return false
}

// httpError lets tests (and non-discordgo senders) simulate transient errors.
type httpError struct{ status int }

func (e *httpError) Error() string { return http.StatusText(e.status) }

// NewTransientError builds a 5xx-compatible error for tests and adapters.
func NewTransientError(status int) error { return &httpError{status: status} }
