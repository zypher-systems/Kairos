package httpapi

import (
	"sync"
	"time"
)

// loginLimiter is a sliding-window counter keyed by client address.
type loginLimiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	limit  int
	window time.Duration
	now    func() time.Time
}

func newLoginLimiter(limit int, window time.Duration) *loginLimiter {
	if limit <= 0 {
		limit = 10
	}
	if window <= 0 {
		window = time.Minute
	}
	return &loginLimiter{
		hits:   make(map[string][]time.Time),
		limit:  limit,
		window: window,
		now:    time.Now,
	}
}

func (l *loginLimiter) allow(key string) bool {
	if key == "" {
		key = "unknown"
	}
	now := l.now()
	cutoff := now.Add(-l.window)
	l.mu.Lock()
	defer l.mu.Unlock()
	// Bound memory: when many addresses are tracked, drop the idle ones.
	if len(l.hits) > 10000 {
		for k, ts := range l.hits {
			if len(ts) == 0 || !ts[len(ts)-1].After(cutoff) {
				delete(l.hits, k)
			}
		}
	}
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.limit {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	return true
}
