package scheduler

import (
	"context"
	"sync"
	"time"

	"github.com/jonthekiller/rss-tracker/internal/store"
)

// Scheduler runs the periodic tick loop. It can be suspended while
// import/restore transactions run.
type Scheduler struct {
	pipeline *Pipeline
	store    *store.FeedStore
	options  SchedulerOptions

	mu        sync.Mutex
	timer     *time.Ticker
	stopCh    chan struct{}
	started   bool
	running   bool
	suspended bool
}

// SchedulerOptions configures the tick loop.
type SchedulerOptions struct {
	// TickIntervalMs is the polling cadence (default 30 s).
	TickIntervalMs int
	// BatchSize caps feeds checked per tick (default 50).
	BatchSize int
}

// NewScheduler creates a Scheduler.
func NewScheduler(pipeline *Pipeline, st *store.FeedStore, options SchedulerOptions) *Scheduler {
	return &Scheduler{pipeline: pipeline, store: st, options: options, stopCh: make(chan struct{})}
}

// Suspend pauses processing (used by import & restore inside a transaction).
func (s *Scheduler) Suspend() {
	s.mu.Lock()
	s.suspended = true
	s.mu.Unlock()
}

// Resume resumes processing.
func (s *Scheduler) Resume() {
	s.mu.Lock()
	s.suspended = false
	s.mu.Unlock()
}

// IsSuspended reports whether processing is paused.
func (s *Scheduler) IsSuspended() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.suspended
}

// Start launches the tick loop and runs a first tick right away.
func (s *Scheduler) Start() {
	s.mu.Lock()
	if s.started {
		s.mu.Unlock()
		return
	}
	s.started = true
	tickInterval := time.Duration(s.options.TickIntervalMs) * time.Millisecond
	if tickInterval == 0 {
		tickInterval = 30_000 * time.Millisecond
	}
	s.timer = time.NewTicker(tickInterval)
	s.mu.Unlock()

	go func() {
		ticker := s.timer
		for {
			select {
			case <-ticker.C:
				s.Tick(context.Background())
			case <-s.stopCh:
				return
			}
		}
	}()

	// First tick right away (async, non-blocking).
	go s.Tick(context.Background())
}

// Stop cancels the tick loop.
func (s *Scheduler) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	if s.started {
		close(s.stopCh)
		s.started = false
	}
}

// Tick runs one processing pass and returns the number of feeds checked.
func (s *Scheduler) Tick(ctx context.Context) int {
	s.mu.Lock()
	if s.running || s.suspended {
		s.mu.Unlock()
		return 0
	}
	s.running = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.running = false
		s.mu.Unlock()
	}()

	now := s.pipeline.now()
	batchSize := s.options.BatchSize
	if batchSize == 0 {
		batchSize = 50
	}
	due, err := s.store.GetFeedsDueNow(now, batchSize)
	if err != nil || len(due) == 0 {
		return 0
	}

	concurrency := 4
	if len(due) < concurrency {
		concurrency = len(due)
	}
	var wg sync.WaitGroup
	index := make(chan int)
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for idx := range index {
				s.pipeline.CheckFeed(ctx, due[idx].ID, now)
			}
		}()
	}
	for i := range due {
		index <- i
	}
	close(index)
	wg.Wait()
	return len(due)
}
