// Package throttle enforces a minimum interval between GitHub API calls per
// installation. Ported from bot/kodiak/throttle.py.
package throttle

import (
	"context"
	"sync"
	"time"
)

// Throttler caps how frequently a given key (typically an installation ID)
// may fire. Each call to Acquire blocks until the next slot is available.
type Throttler struct {
	rate time.Duration

	mu   sync.Mutex
	next map[string]time.Time
}

// New constructs a throttler that allows one call every `rate`.
func New(rate time.Duration) *Throttler {
	return &Throttler{rate: rate, next: make(map[string]time.Time)}
}

// Acquire blocks until the given key's slot is available, or until ctx is
// canceled.
func (t *Throttler) Acquire(ctx context.Context, key string) error {
	t.mu.Lock()
	now := time.Now()
	wait := time.Duration(0)
	if next, ok := t.next[key]; ok && next.After(now) {
		wait = next.Sub(now)
	}
	t.next[key] = now.Add(wait).Add(t.rate)
	t.mu.Unlock()

	if wait == 0 {
		return nil
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
