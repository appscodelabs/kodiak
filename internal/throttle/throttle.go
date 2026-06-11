// Package throttle provides a simple sliding-window rate limiter,
// ported from bot/kodiak/throttle.py (originally based on
// github.com/hallazzang/asyncio-throttle, MIT licensed).
package throttle

import (
	"context"
	"sync"
	"time"
)

type Throttler struct {
	rateLimit     float64
	period        time.Duration
	retryInterval time.Duration

	mu       sync.Mutex
	taskLogs []time.Time
}

func New(rateLimit float64) *Throttler {
	return &Throttler{
		rateLimit:     rateLimit,
		period:        time.Second,
		retryInterval: 10 * time.Millisecond,
	}
}

func (t *Throttler) flush(now time.Time) {
	i := 0
	for i < len(t.taskLogs) && now.Sub(t.taskLogs[i]) > t.period {
		i++
	}
	t.taskLogs = t.taskLogs[i:]
}

func (t *Throttler) Acquire(ctx context.Context) error {
	for {
		now := time.Now()
		t.mu.Lock()
		t.flush(now)
		if float64(len(t.taskLogs)) < t.rateLimit {
			t.taskLogs = append(t.taskLogs, now)
			t.mu.Unlock()
			return nil
		}
		t.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(t.retryInterval):
		}
	}
}

var (
	cacheMu sync.Mutex
	cache   = map[string]*Throttler{}
)

// ForInstallation returns the shared throttler for a GitHub app installation.
func ForInstallation(installationID string) *Throttler {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	t, ok := cache[installationID]
	if !ok {
		// 5000 requests per hour expressed per second.
		t = New(5000.0 / 60.0 / 60.0)
		cache[installationID] = t
	}
	return t
}
