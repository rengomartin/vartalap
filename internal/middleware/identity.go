// Package middleware provides cross-cutting HTTP behaviour.
package middleware

import (
	"context"
	"net/http"
	"strings"
	"time"

	"vartalap/internal/services"
)

// Browsers cap cookie lifetimes at 400 days. Keeping the value explicit means
// the identity survives long gaps between visits instead of expiring with the
// browser session.
const identityCookieMaxAge = 400 * 24 * time.Hour

type deviceIDKey struct{}

// Identity resolves a signed, persistent device identity.
//
// Resolution order:
//  1. the HttpOnly vartalap_id cookie (normal case),
//  2. the JavaScript-readable vartalap_sync mirror (recovers from a lost
//     primary cookie while still having a signed value),
//  3. the X-Vartalap-Device header (used by fetch-based restore flows).
//
// Tokens are HMAC-signed by the server, so a client cannot simply swap in
// somebody else's device ID. A missing or invalid identity gets a fresh one.
func Identity(signer *services.IdentitySigner) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, source := "", ""
			if cookie, err := r.Cookie(services.DeviceIDCookieName); err == nil {
				if _, ok := signer.Verify(cookie.Value); ok {
					token, source = cookie.Value, "primary"
				}
			}
			if token == "" {
				if cookie, err := r.Cookie(services.DeviceSyncCookieName); err == nil {
					if _, ok := signer.Verify(cookie.Value); ok {
						token, source = cookie.Value, "sync"
					}
				}
			}
			if token == "" {
				if value := r.Header.Get(services.DeviceIDHeader); value != "" {
					if _, ok := signer.Verify(value); ok {
						token, source = value, "header"
					}
				}
			}

			deviceID := ""
			if token != "" {
				deviceID, _ = signer.Verify(token)
			}
			if deviceID == "" {
				generated, err := services.NewDeviceID()
				if err != nil {
					http.Error(w, "Internal Server Error", http.StatusInternalServerError)
					return
				}
				deviceID = generated
				token = signer.Issue(generated)
				source = "new"
			}

			// Always repair an incomplete identity. If the HttpOnly cookie was
			// evicted but the sync cookie survived, this re-establishes it, and
			// vice versa. A normal page load also renews the expiry, turning the
			// 400-day cookie into a sliding session for regular visitors.
			refresh := source == "primary" && r.Method == http.MethodGet && r.URL.Path == "/"
			if source != "primary" || refresh {
				setIdentityCookie(w, r, services.DeviceIDCookieName, token, true)
			}
			if refresh {
				setIdentityCookie(w, r, services.DeviceSyncCookieName, token, false)
			} else if synced, err := r.Cookie(services.DeviceSyncCookieName); err != nil || synced.Value != token {
				setIdentityCookie(w, r, services.DeviceSyncCookieName, token, false)
			}

			ctx := context.WithValue(r.Context(), deviceIDKey{}, deviceID)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// SetIdentityCookies writes a verified token into both cookies. It is used by
// the session-restore handler after validating a token supplied from storage.
func SetIdentityCookies(w http.ResponseWriter, r *http.Request, token string) {
	setIdentityCookie(w, r, services.DeviceIDCookieName, token, true)
	setIdentityCookie(w, r, services.DeviceSyncCookieName, token, false)
}

// ClearIdentityCookies removes the persistent identity from this browser.
func ClearIdentityCookies(w http.ResponseWriter, r *http.Request) {
	expireIdentityCookie(w, r, services.DeviceIDCookieName, true)
	expireIdentityCookie(w, r, services.DeviceSyncCookieName, false)
}

func setIdentityCookie(w http.ResponseWriter, r *http.Request, name, token string, httpOnly bool) {
	removeCookieHeader(w, name)
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    token,
		Path:     "/",
		MaxAge:   int(identityCookieMaxAge / time.Second),
		Expires:  time.Now().Add(identityCookieMaxAge),
		HttpOnly: httpOnly,
		Secure:   requestIsSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func expireIdentityCookie(w http.ResponseWriter, r *http.Request, name string, httpOnly bool) {
	removeCookieHeader(w, name)
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(1, 0),
		HttpOnly: httpOnly,
		Secure:   requestIsSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// removeCookieHeader drops an earlier Set-Cookie for the same name. Without
// this, middleware-issued defaults and a handler that replaces (restore) or
// expires (forget) the identity would send two conflicting values.
func removeCookieHeader(w http.ResponseWriter, name string) {
	header := w.Header()
	values := header.Values("Set-Cookie")
	if len(values) == 0 {
		return
	}
	header.Del("Set-Cookie")
	for _, value := range values {
		if strings.HasPrefix(value, name+"=") {
			continue
		}
		header.Add("Set-Cookie", value)
	}
}

// requestIsSecure marks cookies Secure whenever the browser actually used
// HTTPS. Marking cookies Secure on a plain-HTTP deployment makes browsers
// silently discard them, which is exactly how a "remember me" cookie can fail
// on every visit. X-Forwarded-Proto covers TLS-terminating proxies.
func requestIsSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if comma := strings.IndexByte(proto, ','); comma >= 0 {
		proto = proto[:comma]
	}
	return strings.EqualFold(strings.TrimSpace(proto), "https")
}

// DeviceID returns the anonymous device ID placed in the request context.
func DeviceID(ctx context.Context) string {
	value, _ := ctx.Value(deviceIDKey{}).(string)
	return value
}
