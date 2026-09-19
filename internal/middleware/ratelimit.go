package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

// RateLimit is a per-client token bucket. Auth and deposit routes use a tight
// limit; everything else uses a loose one.
func RateLimit(r rate.Limit, burst int) gin.HandlerFunc {
	var (
		mu      sync.Mutex
		clients = map[string]*clientLimiter{}
	)

	// Evict idle buckets so the map cannot grow without bound.
	go func() {
		t := time.NewTicker(5 * time.Minute)
		for range t.C {
			mu.Lock()
			for key, cl := range clients {
				if time.Since(cl.seen) > 10*time.Minute {
					delete(clients, key)
				}
			}
			mu.Unlock()
		}
	}()

	return func(c *gin.Context) {
		key := c.ClientIP()
		if uid := UserID(c); uid.String() != "00000000-0000-0000-0000-000000000000" {
			key = uid.String()
		}

		mu.Lock()
		cl, ok := clients[key]
		if !ok {
			cl = &clientLimiter{limiter: rate.NewLimiter(r, burst)}
			clients[key] = cl
		}
		cl.seen = time.Now()
		allowed := cl.limiter.Allow()
		mu.Unlock()

		if !allowed {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": gin.H{"message": "Too many requests. Slow down a moment.", "code": "rate_limited"},
			})
			return
		}
		c.Next()
	}
}

type clientLimiter struct {
	limiter *rate.Limiter
	seen    time.Time
}
