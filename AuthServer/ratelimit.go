package main

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

// rateLimitEnabled controls whether rate limiting is enforced.
// Set to false (via RATE_LIMIT=false) to disable during development.
var rateLimitEnabled = true

// rateLimitConfig defines the window and max requests for one endpoint.
type rateLimitConfig struct {
	window   time.Duration
	maxReqs  int
}

var limits = map[string]rateLimitConfig{
	"register": {window: 1 * time.Hour, maxReqs: 5},       // 5 accounts per IP per hour
	"login":    {window: 15 * time.Minute, maxReqs: 10},    // 10 attempts per IP per 15 min
	"pubkey":   {window: 1 * time.Minute, maxReqs: 120},    // 120 lookups per IP per minute
}

// bucket tracks request counts within a fixed time window.
type bucket struct {
	mu       sync.Mutex
	count    int
	windowAt time.Time // start of the current window
}

// allow returns true if the request is within the limit, false if it should be rejected.
func (b *bucket) allow(cfg rateLimitConfig) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := time.Now()
	if now.After(b.windowAt.Add(cfg.window)) {
		// Window expired — reset.
		b.count = 0
		b.windowAt = now
	}

	if b.count >= cfg.maxReqs {
		return false
	}
	b.count++
	return true
}

var (
	bucketsMu sync.Mutex
	buckets   = make(map[string]*bucket) // key: "endpoint:ip"
)

func getBucket(endpoint, ip string) *bucket {
	key := endpoint + ":" + ip
	bucketsMu.Lock()
	defer bucketsMu.Unlock()
	b, ok := buckets[key]
	if !ok {
		b = &bucket{windowAt: time.Now()}
		buckets[key] = b
	}
	return b
}

// rateLimit wraps a handler and enforces per-IP rate limiting for the given endpoint.
// Has no effect when rateLimitEnabled is false.
func rateLimit(endpoint string, next http.HandlerFunc) http.HandlerFunc {
	cfg, ok := limits[endpoint]
	if !ok {
		return next
	}

	return func(w http.ResponseWriter, r *http.Request) {
		if !rateLimitEnabled {
			next(w, r)
			return
		}
		ip := clientIP(r)
		b := getBucket(endpoint, ip)
		if !b.allow(cfg) {
			w.Header().Set("Retry-After", fmt.Sprintf("%.0f", cfg.window.Seconds()))
			jsonError(w, "too many requests — slow down", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}

// clientIP extracts the real client IP, respecting X-Forwarded-For for proxies.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Take the first (leftmost) IP — that's the original client.
		for i := 0; i < len(xff); i++ {
			if xff[i] == ',' {
				return xff[:i]
			}
		}
		return xff
	}
	// Strip port from RemoteAddr.
	addr := r.RemoteAddr
	for i := len(addr) - 1; i >= 0; i-- {
		if addr[i] == ':' {
			return addr[:i]
		}
	}
	return addr
}
