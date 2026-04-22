package backup

import (
	"context"
	"fmt"
	"time"
)

// Scheduler triggers the configured function at a given frequency.
type Scheduler struct {
	frequency time.Duration
	nowFunc   func() time.Time
	f         func(context.Context, time.Time)
}

// NewScheduler creates a Scheduler that will invoke f.
// For daily frequency, the first invocation time becomes the anchor for subsequent runs.
func NewScheduler(f func(context.Context, time.Time)) *Scheduler {
	return &Scheduler{
		frequency: 1 * time.Hour,
		nowFunc:   time.Now,
		f:         f,
	}
}

// Start begins invoking the scheduled function in a background goroutine until ctx is canceled.
// It returns immediately after scheduling the first run.
func (s *Scheduler) Start(ctx context.Context) error {
	if s.frequency > 24*time.Hour {
		return fmt.Errorf("frequency must not be >24 hours, but actually is %s", s.frequency)
	}

	now := s.nowFunc().UTC()

	var next time.Time
	if s.frequency < 24*time.Hour {
		next = now
	} else {
		next = now
	}

	go func() {
		delay := time.Until(next)

		if delay < 0 {
			delay = 0
		}

		timer := time.NewTimer(delay)
		defer timer.Stop()

		select {
		case <-ctx.Done():
			return
		case t := <-timer.C:
			s.f(ctx, t)
		}

		ticker := time.NewTicker(s.frequency)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case t := <-ticker.C:
				s.f(ctx, t)
			}
		}
	}()

	return nil
}
