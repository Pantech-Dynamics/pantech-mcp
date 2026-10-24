package mcp

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// limiter is an in-memory token bucket per key (an IP, or a hash of a key).
// Buckets idle for 10 minutes are dropped, and there are never more than
// maxBuckets, so memory stays bounded even when a flood of addresses or
// made-up keys arrives faster than buckets go idle.
type limiter struct {
	perMinute  int
	burst      int
	maxBuckets int
	mu         sync.Mutex
	buckets    map[string]*bucket
	lastSweep  time.Time
	now        func() time.Time
}

type bucket struct {
	lim  *rate.Limiter
	seen time.Time
}

const bucketIdle = 10 * time.Minute

// defaultMaxBuckets bounds a limiter's memory (a bucket is ~200 bytes).
const defaultMaxBuckets = 100_000

// newLimiter allows perMinute requests a minute per key, with bursts of
// burst. perMinute <= 0 turns it off.
func newLimiter(perMinute, burst int) *limiter {
	if burst <= 0 {
		burst = max(1, perMinute/2)
	}
	return &limiter{perMinute: perMinute, burst: burst, maxBuckets: defaultMaxBuckets, buckets: map[string]*bucket{}, now: time.Now}
}

// allow reports whether key may make a request now.
func (l *limiter) allow(key string) bool {
	if l == nil || l.perMinute <= 0 {
		return true
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if now.Sub(l.lastSweep) > time.Minute {
		for k, b := range l.buckets {
			if now.Sub(b.seen) > bucketIdle {
				delete(l.buckets, k)
			}
		}
		l.lastSweep = now
	}
	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= l.maxBuckets {
			l.evict()
		}
		b = &bucket{lim: rate.NewLimiter(rate.Limit(float64(l.perMinute)/60), l.burst)}
		l.buckets[key] = b
	}
	b.seen = now
	return b.lim.AllowN(now, 1)
}

// evict makes room when the limiter is full: buckets that are full again
// (their caller has been quiet long enough to be back at their burst) go
// first, as forgetting them changes nothing; if that frees too little, a
// tenth of the rest, chosen at random by map order.
func (l *limiter) evict() {
	now := l.now()
	for k, b := range l.buckets {
		if b.lim.TokensAt(now) >= float64(l.burst) {
			delete(l.buckets, k)
		}
	}
	if len(l.buckets) < l.maxBuckets {
		return
	}
	n := max(1, l.maxBuckets/10)
	for k := range l.buckets {
		if n == 0 {
			break
		}
		delete(l.buckets, k)
		n--
	}
}

// exhausted reports whether key has no request left right now, without
// using one.
func (l *limiter) exhausted(key string) bool {
	if l == nil || l.perMinute <= 0 {
		return false
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	return ok && b.lim.TokensAt(now) < 1
}
