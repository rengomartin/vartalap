package middleware

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"time"
)

const (
	// CSRFCookieName holds a double-submit CSRF token. It is intentionally
	// readable by the page's own JavaScript but not by third-party origins.
	CSRFCookieName = "vartalap_csrf"
	// CSRFHeaderName is required on every state-changing request.
	CSRFHeaderName = "X-CSRF-Token"
)

type csrfKey struct{}

// CSRF issues a per-browser token and exposes it through the request context.
func CSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := ""
		if cookie, err := r.Cookie(CSRFCookieName); err == nil && len(cookie.Value) >= 32 {
			token = cookie.Value
		}
		if token == "" {
			token = newCSRFToken()
			http.SetCookie(w, &http.Cookie{
				Name:     CSRFCookieName,
				Value:    token,
				Path:     "/",
				MaxAge:   int((400 * 24 * time.Hour) / time.Second),
				Expires:  time.Now().Add(400 * 24 * time.Hour),
				HttpOnly: false,
				Secure:   requestIsSecure(r),
				SameSite: http.SameSiteLaxMode,
			})
		}
		ctx := context.WithValue(r.Context(), csrfKey{}, token)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// CSRFToken returns the token placed in the request context.
func CSRFToken(ctx context.Context) string {
	value, _ := ctx.Value(csrfKey{}).(string)
	return value
}

// VerifyCSRF checks the double-submit token. The header is checked first so
// raw-body uploads are never consumed by form parsing.
func VerifyCSRF(r *http.Request) bool {
	cookie, err := r.Cookie(CSRFCookieName)
	if err != nil || cookie.Value == "" {
		return false
	}
	provided := r.Header.Get(CSRFHeaderName)
	if provided == "" {
		provided = r.FormValue("_csrf")
	}
	if len(provided) != len(cookie.Value) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(cookie.Value)) == 1
}

func newCSRFToken() string {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		panic("vartalap: generate csrf token: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}
