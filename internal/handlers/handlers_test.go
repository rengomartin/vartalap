package handlers_test

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"vartalap/internal/services"
)

func TestFirstVisitSetsCookieAndShowsWelcome(t *testing.T) {
	app, _, _ := newApp(t, testConfig(t))
	recorder := request(app, http.MethodGet, "/", "", "")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), "Join the room") {
		t.Fatalf("welcome page not rendered: %s", recorder.Body.String())
	}
	deviceID, ok := testSigner.Verify(cookieValue(t, recorder))
	if !ok || !services.IsValidDeviceID(deviceID) {
		t.Fatalf("invalid signed device cookie %q", cookieValue(t, recorder))
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) < 2 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("primary cookie must be HttpOnly and SameSite=Lax: %+v", cookies)
	}
	if cookies[1].Name != services.DeviceSyncCookieName || cookies[1].HttpOnly {
		t.Fatalf("sync cookie must be readable by scripts: %+v", cookies[1])
	}
}

func TestJoinRestoresNameAndChatPage(t *testing.T) {
	app, store, _ := newApp(t, testConfig(t))
	deviceID := newDevice(t)

	joined := request(app, http.MethodPost, "/join",
		formBody(url.Values{"display_name": {"  Aap  "}}), deviceID)
	if joined.Code != http.StatusOK {
		t.Fatalf("join status = %d", joined.Code)
	}
	body := joined.Body.String()
	if !strings.Contains(body, `id="chat-view"`) || !strings.Contains(body, "Aap") {
		t.Fatalf("chat page missing: %s", body[:min(len(body), 500)])
	}

	user, found, err := store.GetUser(context.Background(), deviceID)
	if err != nil || !found || user.DisplayName != "Aap" {
		t.Fatalf("user after join: %+v found=%v err=%v", user, found, err)
	}

	revisited := request(app, http.MethodGet, "/", "", deviceID)
	if !strings.Contains(revisited.Body.String(), "Aap") {
		t.Fatal("display name not restored on revisit")
	}
}

func TestJoinRejectsInvalidName(t *testing.T) {
	app, _, _ := newApp(t, testConfig(t))
	recorder := request(app, http.MethodPost, "/join",
		formBody(url.Values{"display_name": {"   "}}), newDevice(t))
	if !strings.Contains(recorder.Body.String(), "choose a display name") {
		t.Fatalf("validation error not shown: %s", recorder.Body.String())
	}
}

func TestHistoryPagination(t *testing.T) {
	cfg := testConfig(t)
	cfg.HistoryLimit = 2
	app, store, _ := newApp(t, cfg)
	deviceID := newDevice(t)
	seedUser(t, store, deviceID, "Aap")

	var ids [5]int64
	for index := range ids {
		ids[index] = seedMessage(t, store, deviceID, "Aap", strings.Repeat("x", index+1)).ID
	}

	recorder := request(app, http.MethodGet, "/history?before=invalid", "", deviceID)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("invalid cursor status = %d", recorder.Code)
	}

	recorder = request(app, http.MethodGet,
		"/history?before="+strconv.FormatInt(ids[3], 10), "", deviceID)
	body := recorder.Body.String()
	if !strings.Contains(body, "xx") || !strings.Contains(body, "xxx") || strings.Contains(body, ">x<") {
		t.Fatalf("unexpected history page: %s", body)
	}
	if !strings.Contains(body, `hx-get="/history?before=`+strconv.FormatInt(ids[1], 10)+`"`) {
		t.Fatalf("next cursor missing: %s", body)
	}
}

func TestPageAndHistoryMarkOwnMessagesWithoutRightAlignment(t *testing.T) {
	app, store, _ := newApp(t, testConfig(t))
	deviceID := newDevice(t)
	seedUser(t, store, deviceID, "Aap")
	message := seedMessage(t, store, deviceID, "Aap", "my own line")

	page := request(app, http.MethodGet, "/", "", deviceID).Body.String()
	if !strings.Contains(page, `data-mine="true"`) || !strings.Contains(page, "my own line") {
		t.Fatalf("own message not marked on page: %s", page)
	}
	if strings.Contains(page, "justify-end") {
		t.Fatalf("own message aligned right on page: %s", page)
	}

	history := request(app, http.MethodGet,
		"/history?before="+strconv.FormatInt(message.ID+1, 10), "", deviceID).Body.String()
	if !strings.Contains(history, `data-mine="true"`) {
		t.Fatalf("own message not marked in history: %s", history)
	}
	if strings.Contains(history, "justify-end") {
		t.Fatalf("own message aligned right in history: %s", history)
	}
}

func TestUpdateName(t *testing.T) {
	app, store, _ := newApp(t, testConfig(t))
	deviceID := newDevice(t)
	seedUser(t, store, deviceID, "Aap")

	recorder := request(app, http.MethodPost, "/name",
		formBody(url.Values{"display_name": {"Naya Naam"}}), deviceID)
	body := recorder.Body.String()
	if !strings.Contains(body, `id="name-display"`) || !strings.Contains(body, "Naya Naam") {
		t.Fatalf("name fragment missing: %s", body)
	}
	user, _, err := store.GetUser(context.Background(), deviceID)
	if err != nil || user.DisplayName != "Naya Naam" {
		t.Fatalf("name not persisted: %+v err=%v", user, err)
	}
}

func TestStaticAssets(t *testing.T) {
	app, _, _ := newApp(t, testConfig(t))

	recorder := request(app, http.MethodGet, "/static/js/app.js", "", "")
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Header().Get("Content-Type"), "javascript") {
		t.Fatalf("static js: status=%d type=%q", recorder.Code, recorder.Header().Get("Content-Type"))
	}

	compressed := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/static/css/app.css", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	app.ServeHTTP(compressed, req)
	if compressed.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("expected gzip encoding, got %q", compressed.Header().Get("Content-Encoding"))
	}
	reader, err := gzip.NewReader(compressed.Body)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer reader.Close()
	content, err := io.ReadAll(reader)
	if err != nil || !strings.Contains(string(content), "msg-grouped") {
		t.Fatalf("decompressed asset invalid: %v", err)
	}

	carbon := request(app, http.MethodGet, "/static/css/carbon.css", "", "")
	if carbon.Code != http.StatusOK || !strings.Contains(carbon.Body.String(), ".cds--btn--primary") {
		t.Fatalf("carbon css missing: status=%d", carbon.Code)
	}

	font := request(app, http.MethodGet, "/static/fonts/IBMPlexSans-Regular-Latin1.woff2", "", "")
	if font.Code != http.StatusOK || !strings.Contains(font.Header().Get("Content-Type"), "woff2") {
		t.Fatalf("plex font missing: status=%d type=%q", font.Code, font.Header().Get("Content-Type"))
	}

	logo := request(app, http.MethodGet, "/static/logo-mark.svg", "", "")
	if logo.Code != http.StatusOK || !strings.Contains(logo.Header().Get("Content-Type"), "svg") {
		t.Fatalf("logo missing: status=%d type=%q", logo.Code, logo.Header().Get("Content-Type"))
	}
}

func TestRestoreReissuesIdentityFromSignedToken(t *testing.T) {
	app, store, _ := newApp(t, testConfig(t))
	deviceID := newDevice(t)
	seedUser(t, store, deviceID, "Aap")

	token := testSigner.Issue(deviceID)
	recorder := request(app, http.MethodPost, "/restore",
		formBody(url.Values{"token": {token}}), "")
	if recorder.Code != http.StatusSeeOther || recorder.Header().Get("Location") != "/" {
		t.Fatalf("restore redirect: status=%d location=%q", recorder.Code, recorder.Header().Get("Location"))
	}

	primary := cookieByName(recorder, services.DeviceIDCookieName)
	if primary == nil || primary.Value != token {
		t.Fatalf("restore did not reissue the signed cookie: %+v", recorder.Result().Cookies())
	}

	// A follow-up browser navigation with the restored HttpOnly cookie should
	// land straight in the chat with the saved display name.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(primary)
	followUp := httptest.NewRecorder()
	app.ServeHTTP(followUp, req)
	if !strings.Contains(followUp.Body.String(), `id="chat-view"`) ||
		!strings.Contains(followUp.Body.String(), "Aap") {
		t.Fatalf("restored session did not render chat: %s", followUp.Body.String()[:min(len(followUp.Body.String()), 400)])
	}
}

func TestRestoreRejectsInvalidToken(t *testing.T) {
	app, _, _ := newApp(t, testConfig(t))
	recorder := request(app, http.MethodPost, "/restore",
		formBody(url.Values{"token": {"v1.forged.signature"}}), "")
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "Join the room") {
		t.Fatalf("invalid token should return the welcome page: %d", recorder.Code)
	}
}

// TestNoIdentityCookieStillWorks is the regression test for the original
// report: a returning browser that lost its cookie must not silently become a
// brand-new visitor when another signed copy exists.
func TestIdentitySurvivesMissingPrimaryCookie(t *testing.T) {
	app, store, _ := newApp(t, testConfig(t))
	deviceID := newDevice(t)
	seedUser(t, store, deviceID, "Aap")
	token := testSigner.Issue(deviceID)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: services.DeviceSyncCookieName, Value: token})
	recorder := httptest.NewRecorder()
	app.ServeHTTP(recorder, req)

	body := recorder.Body.String()
	if !strings.Contains(body, `id="chat-view"`) || !strings.Contains(body, "Aap") {
		t.Fatalf("sync cookie did not restore the session: %s", body[:min(len(body), 400)])
	}
	if cookieByName(recorder, services.DeviceIDCookieName) == nil {
		t.Fatal("primary cookie should be reissued from the sync cookie")
	}
}

func TestForgetClearsCookies(t *testing.T) {
	app, _, _ := newApp(t, testConfig(t))
	recorder := request(app, http.MethodPost, "/forget", "", newDevice(t))
	if recorder.Code != http.StatusSeeOther {
		t.Fatalf("forget status = %d", recorder.Code)
	}
	for _, name := range []string{services.DeviceIDCookieName, services.DeviceSyncCookieName} {
		cookie := cookieByName(recorder, name)
		if cookie == nil || cookie.MaxAge >= 0 {
			t.Fatalf("cookie %q was not expired: %+v", name, cookie)
		}
	}
}

func TestHealthCheck(t *testing.T) {
	app, _, _ := newApp(t, testConfig(t))
	recorder := request(app, http.MethodGet, "/healthz", "", "")
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"ok"`) {
		t.Fatalf("health check: %d %s", recorder.Code, recorder.Body.String())
	}
}
