package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KotlinDevIanBore/tradederivplus/internal/config"
)

// Refresh tokens live in httpOnly cookies rather than in the JSON body, so a
// long-lived credential is never reachable from JavaScript and cannot be
// exfiltrated by an XSS bug.
//
// The two sessions get different cookie names AND different paths, so an
// admin cookie is never even sent on a user route and vice versa. The browser
// scopes them apart for us.
const (
	userRefreshCookie  = "tdp_refresh"
	userRefreshPath    = "/api/v1/auth"
	adminRefreshCookie = "tdp_admin_refresh"
	adminRefreshPath   = "/api/v1/admin"
)

// setRefreshCookie writes the refresh token.
//
// SameSite=Strict is deliberate and is the attribute NOT to relax when this
// does not work in production. It means the cookie is only sent when the
// request's site matches the cookie's. Locally the web app on :5173 and the
// API on :8080 share a site because the port is not part of one, so it works.
// In production the web app and API must share a registrable domain, or sit
// behind one reverse proxy, or the cookie is never sent and every session dies
// at the access token's TTL. Dropping to SameSite=None to "fix" that reopens
// the CSRF exposure this cookie exists to close.
func setRefreshCookie(c *gin.Context, cfg *config.Config, name, path, token string, ttl time.Duration) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    token,
		Path:     path,
		Domain:   cfg.CookieDomain,
		MaxAge:   int(ttl.Seconds()),
		HttpOnly: true,
		Secure:   cfg.CookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
}

// clearRefreshCookie expires the cookie. The attributes must match the ones it
// was set with or the browser treats it as a different cookie and leaves the
// original in place.
func clearRefreshCookie(c *gin.Context, cfg *config.Config, name, path string) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     path,
		Domain:   cfg.CookieDomain,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   cfg.CookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
}

func readCookie(c *gin.Context, name string) string {
	v, err := c.Cookie(name)
	if err != nil {
		return ""
	}
	return v
}
