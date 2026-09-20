package api

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// APIRateLimiter caps how many requests one signed-in user may make per
// window. It is a coarse abuse guard (runaway scripts, a stolen session being
// scraped), sized well above what the UI does even while polling.
type APIRateLimiter struct {
	mu      sync.Mutex
	windows map[int64]*window
	max     int
	span    time.Duration
}

type window struct {
	count   int
	resetAt time.Time
}

func NewAPIRateLimiter(max int, span time.Duration) *APIRateLimiter {
	return &APIRateLimiter{windows: make(map[int64]*window), max: max, span: span}
}

// Middleware must run after SessionAuth (it keys on the user id).
func (l *APIRateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := CurrentUser(c).ID
		now := time.Now()

		l.mu.Lock()
		w := l.windows[id]
		if w == nil || now.After(w.resetAt) {
			w = &window{resetAt: now.Add(l.span)}
			l.windows[id] = w
		}
		w.count++
		over := w.count > l.max
		retry := int(time.Until(w.resetAt).Seconds()) + 1
		l.mu.Unlock()

		if over {
			c.Header("Retry-After", strconv.Itoa(retry))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "too many requests, slow down",
			})
			return
		}
		c.Next()
	}
}

// LoginRateLimiter blocks an IP after too many failed login attempts in a
// short window, to slow down password guessing against the single admin
// account.
type LoginRateLimiter struct {
	mu        sync.Mutex
	attempts  map[string][]time.Time
	max       int
	window    time.Duration
	lastSweep time.Time
}

func NewLoginRateLimiter(max int, window time.Duration) *LoginRateLimiter {
	return &LoginRateLimiter{
		attempts: make(map[string][]time.Time),
		max:      max,
		window:   window,
	}
}

func (l *LoginRateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		now := time.Now()

		l.mu.Lock()
		l.sweep(now)
		recent := l.attempts[ip][:0]
		for _, t := range l.attempts[ip] {
			if now.Sub(t) < l.window {
				recent = append(recent, t)
			}
		}
		if len(recent) == 0 {
			delete(l.attempts, ip) // never let the map grow with one-off visitors
		} else {
			l.attempts[ip] = recent
		}

		if len(recent) >= l.max {
			l.mu.Unlock()
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "too many login attempts, try again later",
			})
			return
		}
		l.mu.Unlock()

		c.Next()

		if c.Writer.Status() == http.StatusUnauthorized {
			l.mu.Lock()
			l.attempts[ip] = append(l.attempts[ip], now)
			l.mu.Unlock()
		}
	}
}

// sweep drops every address whose failures have all expired, at most once per
// window. Without it an address that fails once and never returns would stay
// in the map forever. The caller holds l.mu.
func (l *LoginRateLimiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < l.window {
		return
	}
	l.lastSweep = now
	for ip, times := range l.attempts {
		live := false
		for _, t := range times {
			if now.Sub(t) < l.window {
				live = true
				break
			}
		}
		if !live {
			delete(l.attempts, ip)
		}
	}
}
