package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"vartalap/internal/services"
)

func newTestSigner() *services.IdentitySigner {
	return services.NewIdentitySigner([]byte("middleware-test-secret-32-bytes!!"))
}

func identityHandler(seen *string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*seen = DeviceID(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})
}

func TestIdentityIssuesSignedCookies(t *testing.T) {
	signer := newTestSigner()
	var seen string
	handler := Identity(signer)(identityHandler(&seen))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	cookies := recorder.Result().Cookies()
	if len(cookies) != 2 {
		t.Fatalf("want primary and sync cookies, got %+v", cookies)
	}
	primary := cookieByName(cookies, services.DeviceIDCookieName)
	sync := cookieByName(cookies, services.DeviceSyncCookieName)
	if primary == nil || sync == nil {
		t.Fatalf("missing identity cookies: %+v", cookies)
	}
	if !primary.HttpOnly || sync.HttpOnly {
		t.Fatalf("unexpected HttpOnly flags: primary=%v sync=%v", primary.HttpOnly, sync.HttpOnly)
	}
	if primary.Value != sync.Value {
		t.Fatal("primary and sync cookies must carry the same token")
	}
	if primary.SameSite != http.SameSiteLaxMode {
		t.Fatalf("SameSite = %v", primary.SameSite)
	}
	if primary.MaxAge < int((399*24*time.Hour)/time.Second) {
		t.Fatalf("cookie lifetime too short: %d seconds", primary.MaxAge)
	}
	deviceID, ok := signer.Verify(primary.Value)
	if !ok || deviceID != seen {
		t.Fatalf("token verification failed: ok=%v deviceID=%q seen=%q", ok, deviceID, seen)
	}
}

func TestIdentityRecoversFromSyncCookie(t *testing.T) {
	signer := newTestSigner()
	deviceID, err := services.NewDeviceID()
	if err != nil {
		t.Fatal(err)
	}
	token := signer.Issue(deviceID)

	var seen string
	handler := Identity(signer)(identityHandler(&seen))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(&http.Cookie{Name: services.DeviceSyncCookieName, Value: token})

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if seen != deviceID {
		t.Fatalf("device ID = %q, want %q", seen, deviceID)
	}
	primary := cookieByName(recorder.Result().Cookies(), services.DeviceIDCookieName)
	if primary == nil || primary.Value != token {
		t.Fatalf("primary cookie was not repaired: %+v", recorder.Result().Cookies())
	}
}

func TestIdentityRefreshesExpiryOnHomeVisit(t *testing.T) {
	signer := newTestSigner()
	deviceID, err := services.NewDeviceID()
	if err != nil {
		t.Fatal(err)
	}
	token := signer.Issue(deviceID)

	handler := Identity(signer)(identityHandler(new(string)))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(&http.Cookie{Name: services.DeviceIDCookieName, Value: token})
	request.AddCookie(&http.Cookie{Name: services.DeviceSyncCookieName, Value: token})

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	primary := cookieByName(recorder.Result().Cookies(), services.DeviceIDCookieName)
	if primary == nil || primary.Value != token || primary.MaxAge <= 0 {
		t.Fatalf("home visit should renew the identity expiry: %+v", recorder.Result().Cookies())
	}
}

func TestIdentityRejectsForgedToken(t *testing.T) {
	signer := newTestSigner()
	other := services.NewIdentitySigner([]byte("a-different-secret-entirely-32b!"))
	target, err := services.NewDeviceID()
	if err != nil {
		t.Fatal(err)
	}

	var seen string
	handler := Identity(signer)(identityHandler(&seen))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(&http.Cookie{Name: services.DeviceIDCookieName, Value: other.Issue(target)})

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if seen == target {
		t.Fatal("forged token should not authenticate as its embedded device")
	}
	primary := cookieByName(recorder.Result().Cookies(), services.DeviceIDCookieName)
	if primary == nil || primary.Value == "" {
		t.Fatal("a fresh identity cookie should replace the forged one")
	}
}

func TestIdentityMarksCookiesSecureOnlyForHTTPS(t *testing.T) {
	signer := newTestSigner()
	handler := Identity(signer)(identityHandler(new(string)))

	plain := httptest.NewRecorder()
	handler.ServeHTTP(plain, httptest.NewRequest(http.MethodGet, "/", nil))
	if cookieByName(plain.Result().Cookies(), services.DeviceIDCookieName).Secure {
		t.Fatal("HTTP cookies must not be marked Secure; browsers would discard them")
	}

	proxied := httptest.NewRequest(http.MethodGet, "/", nil)
	proxied.Header.Set("X-Forwarded-Proto", "https")
	secure := httptest.NewRecorder()
	handler.ServeHTTP(secure, proxied)
	if !cookieByName(secure.Result().Cookies(), services.DeviceIDCookieName).Secure {
		t.Fatal("HTTPS cookies should be marked Secure")
	}
}

func cookieByName(cookies []*http.Cookie, name string) *http.Cookie {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie
		}
	}
	return nil
}
