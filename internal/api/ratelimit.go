package api

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	rlRate  = 1.0 // tokens replenished per second (60 req/min sustained)
	rlBurst = 20  // burst capacity
)

type rlVisitor struct {
	tokens   float64
	lastSeen time.Time
}

type rateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*rlVisitor
}

var globalLimiter = &rateLimiter{visitors: make(map[string]*rlVisitor)}

func init() {
	go globalLimiter.cleanupLoop()
}

func (rl *rateLimiter) allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	now := time.Now()
	v, ok := rl.visitors[ip]
	if !ok {
		rl.visitors[ip] = &rlVisitor{tokens: rlBurst - 1, lastSeen: now}
		return true
	}
	v.tokens += now.Sub(v.lastSeen).Seconds() * rlRate
	if v.tokens > rlBurst {
		v.tokens = rlBurst
	}
	v.lastSeen = now
	if v.tokens < 1 {
		return false
	}
	v.tokens--
	return true
}

func (rl *rateLimiter) cleanupLoop() {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for range t.C {
		rl.mu.Lock()
		for ip, v := range rl.visitors {
			if time.Since(v.lastSeen) > 10*time.Minute {
				delete(rl.visitors, ip)
			}
		}
		rl.mu.Unlock()
	}
}

// RateLimitMiddleware limits each client IP to rlBurst requests initially,
// refilling at rlRate requests/second. OPTIONS preflight requests are exempt.
func RateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		if !globalLimiter.allow(clientIP(r)) {
			http.Error(w, `{"error":"rate limit exceeded"}`, http.StatusTooManyRequests)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// clientIP returns the real client IP, preferring X-Forwarded-For (set by
// nginx-ingress) over the raw remote address.
func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		ip := xff
		if i := strings.IndexByte(xff, ','); i >= 0 {
			ip = xff[:i]
		}
		if trimmed := strings.TrimSpace(ip); trimmed != "" {
			return trimmed
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
