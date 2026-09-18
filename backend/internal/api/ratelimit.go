package api

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// LoginRateLimiter blocks an IP after too many failed login attempts in a
// short window, to slow down password guessing against the single admin
// account.
type LoginRateLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
	max      int
	window   time.Duration
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
		recent := l.attempts[ip][:0]
		for _, t := range l.attempts[ip] {
			if now.Sub(t) < l.window {
				recent = append(recent, t)
			}
		}
		l.attempts[ip] = recent

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
