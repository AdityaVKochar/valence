package submissions

import (
	"sync"
	"time"
)

type RateLimiter struct {
	mu      sync.Mutex
	every   time.Duration
	burst   float64
	now     func() time.Time
	buckets map[int64]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func NewRateLimiter(every time.Duration, burst int) *RateLimiter {
	return &RateLimiter{every: every, burst: float64(max(burst, 1)), now: time.Now, buckets: map[int64]*bucket{}}
}

func (l *RateLimiter) Allow(userID int64) (bool, time.Duration) {
	if l.every <= 0 {
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.buckets[userID]
	if !ok {
		if len(l.buckets) > 10000 {
			l.prune(now)
		}
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[userID] = b
	}
	b.tokens = min(l.burst, b.tokens+float64(now.Sub(b.last))/float64(l.every))
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration((1 - b.tokens) * float64(l.every))
	return false, wait
}

func (l *RateLimiter) prune(now time.Time) {
	for id, b := range l.buckets {
		if b.tokens+float64(now.Sub(b.last))/float64(l.every) >= l.burst {
			delete(l.buckets, id)
		}
	}
}
