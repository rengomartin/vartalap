package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"vartalap/internal/services"
)

func dialWS(t *testing.T, server *httptest.Server, deviceID string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	header := http.Header{}
	header.Set("Origin", server.URL)
	header.Add("Cookie", services.DeviceIDCookieName+"="+testSigner.Issue(deviceID))
	conn, response, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		if response != nil {
			t.Fatalf("dial websocket: %v (status %d)", err, response.StatusCode)
		}
		t.Fatalf("dial websocket: %v", err)
	}
	return conn
}

func readWS(t *testing.T, conn *websocket.Conn) string {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	_, payload, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read websocket: %v", err)
	}
	return string(payload)
}

func sendWS(t *testing.T, conn *websocket.Conn, payload string) {
	t.Helper()
	if err := conn.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
		t.Fatalf("write websocket: %v", err)
	}
}

func TestWSBroadcastPresenceAndServerSideName(t *testing.T) {
	app, store, _ := newApp(t, testConfig(t))
	deviceA, deviceB := newDevice(t), newDevice(t)
	seedUser(t, store, deviceA, "Aap")
	seedUser(t, store, deviceB, "Bhai")

	server := httptest.NewServer(app)
	defer server.Close()

	connA := dialWS(t, server, deviceA)
	defer connA.Close()
	if message := readWS(t, connA); !strings.Contains(message, `id="online-count"`) || !strings.Contains(message, ">1<") {
		t.Fatalf("initial presence: %s", message)
	}

	connB := dialWS(t, server, deviceB)
	defer connB.Close()
	if message := readWS(t, connB); !strings.Contains(message, ">2<") {
		t.Fatalf("second client presence: %s", message)
	}
	if message := readWS(t, connA); !strings.Contains(message, ">2<") {
		t.Fatalf("first client presence update: %s", message)
	}

	sendWS(t, connA, `{"content":"hello from A","display_name":"Hacker"}`)
	own := readWS(t, connA)
	other := readWS(t, connB)
	if !strings.Contains(own, "hello from A") || !strings.Contains(own, `data-mine="true"`) || !strings.Contains(own, "Aap") {
		t.Fatalf("sender message: %s", own)
	}
	if strings.Contains(own, "Hacker") {
		t.Fatalf("client-supplied name was trusted: %s", own)
	}
	if !strings.Contains(other, "hello from A") || !strings.Contains(other, `data-mine="false"`) || !strings.Contains(other, "Aap") {
		t.Fatalf("recipient message: %s", other)
	}

	messages, err := store.LatestMessages(context.Background(), 1)
	if err != nil || len(messages) != 1 || messages[0].Content != "hello from A" {
		t.Fatalf("message not persisted: %+v err=%v", messages, err)
	}
}

func TestWSNonceIsEchoedForOptimisticReconciliation(t *testing.T) {
	app, store, _ := newApp(t, testConfig(t))
	deviceID := newDevice(t)
	seedUser(t, store, deviceID, "Aap")

	server := httptest.NewServer(app)
	defer server.Close()
	conn := dialWS(t, server, deviceID)
	defer conn.Close()
	readWS(t, conn) // presence

	sendWS(t, conn, `{"content":"hello","nonce":"abc-123"}`)
	if message := readWS(t, conn); !strings.Contains(message, `data-nonce="abc-123"`) {
		t.Fatalf("nonce not echoed: %s", message)
	}

	sendWS(t, conn, `{"content":"again","nonce":"<b>bad</b>"}`)
	if message := readWS(t, conn); !strings.Contains(message, `data-nonce=""`) {
		t.Fatalf("unsafe nonce should be dropped: %s", message)
	}
}

func TestWSPresenceWithTabsAndReconnection(t *testing.T) {
	app, store, _ := newApp(t, testConfig(t))
	deviceA, deviceB := newDevice(t), newDevice(t)
	seedUser(t, store, deviceA, "Aap")
	seedUser(t, store, deviceB, "Bhai")

	server := httptest.NewServer(app)
	defer server.Close()

	connB := dialWS(t, server, deviceB)
	defer connB.Close()
	readWS(t, connB) // 1 online

	tabOne := dialWS(t, server, deviceA)
	readWS(t, tabOne) // 2 online
	if message := readWS(t, connB); !strings.Contains(message, ">2<") {
		t.Fatalf("presence after A joined: %s", message)
	}

	tabTwo := dialWS(t, server, deviceA)
	readWS(t, tabTwo) // still 2 distinct devices
	if message := readWS(t, connB); !strings.Contains(message, ">2<") {
		t.Fatalf("second tab should not increase unique count: %s", message)
	}

	tabOne.Close()
	if message := readWS(t, connB); !strings.Contains(message, ">2<") {
		t.Fatalf("closing one tab should keep device online: %s", message)
	}
	tabTwo.Close()
	if message := readWS(t, connB); !strings.Contains(message, ">1<") {
		t.Fatalf("closing last tab should decrement count: %s", message)
	}

	reconnected := dialWS(t, server, deviceA)
	defer reconnected.Close()
	if message := readWS(t, connB); !strings.Contains(message, ">2<") {
		t.Fatalf("reconnect presence: %s", message)
	}
	sendWS(t, reconnected, `{"content":"back again"}`)
	if message := readWS(t, connB); !strings.Contains(message, "back again") {
		t.Fatalf("message after reconnect: %s", message)
	}
}

func TestWSOriginValidation(t *testing.T) {
	app, store, _ := newApp(t, testConfig(t))
	deviceID := newDevice(t)
	seedUser(t, store, deviceID, "Aap")
	server := httptest.NewServer(app)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	header := http.Header{}
	header.Set("Origin", "http://evil.example")
	header.Add("Cookie", services.DeviceIDCookieName+"="+testSigner.Issue(deviceID))
	_, response, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err == nil {
		t.Fatal("cross-origin WebSocket upgrade should be rejected")
	}
	if response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %+v", response)
	}
}

func TestWSValidationMalformedAndRateLimit(t *testing.T) {
	cfg := testConfig(t)
	cfg.RatePerSecond = 0.01
	cfg.RateBurst = 1
	app, store, _ := newApp(t, cfg)
	deviceID := newDevice(t)
	seedUser(t, store, deviceID, "Aap")

	server := httptest.NewServer(app)
	defer server.Close()
	conn := dialWS(t, server, deviceID)
	defer conn.Close()
	readWS(t, conn) // presence

	sendWS(t, conn, "not-json")
	if message := readWS(t, conn); !strings.Contains(message, "could not be read") {
		t.Fatalf("malformed payload: %s", message)
	}

	sendWS(t, conn, `{"content":"   "}`)
	if message := readWS(t, conn); !strings.Contains(message, "cannot be empty") {
		t.Fatalf("empty message: %s", message)
	}

	sendWS(t, conn, `{"content":"`+strings.Repeat("x", 2001)+`"}`)
	if message := readWS(t, conn); !strings.Contains(message, "at most 2000") {
		t.Fatalf("oversized message: %s", message)
	}

	sendWS(t, conn, `{"content":"first"}`)
	if message := readWS(t, conn); !strings.Contains(message, "first") {
		t.Fatalf("first valid message: %s", message)
	}

	sendWS(t, conn, `{"content":"second"}`)
	if message := readWS(t, conn); !strings.Contains(message, "Slow down") {
		t.Fatalf("rate limit notice: %s", message)
	}
}

func TestWSEscapesUserContent(t *testing.T) {
	app, store, _ := newApp(t, testConfig(t))
	deviceID := newDevice(t)
	seedUser(t, store, deviceID, "<b>Evil</b>")

	server := httptest.NewServer(app)
	defer server.Close()
	conn := dialWS(t, server, deviceID)
	defer conn.Close()
	readWS(t, conn) // presence

	sendWS(t, conn, `{"content":"<script>alert(1)</script>"}`)
	message := readWS(t, conn)
	if strings.Contains(message, "<script>") || strings.Contains(message, "<b>Evil</b>") {
		t.Fatalf("user content was not escaped: %s", message)
	}
	if !strings.Contains(message, "&lt;script&gt;") || !strings.Contains(message, "&lt;b&gt;Evil&lt;/b&gt;") {
		t.Fatalf("escaped content missing: %s", message)
	}
}

func TestWSRequiresDisplayName(t *testing.T) {
	app, _, _ := newApp(t, testConfig(t))
	server := httptest.NewServer(app)
	defer server.Close()
	deviceID := newDevice(t)

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
	header := http.Header{}
	header.Set("Origin", server.URL)
	header.Add("Cookie", services.DeviceIDCookieName+"="+testSigner.Issue(deviceID))
	_, response, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for visitor without a name, got response=%v err=%v", response, err)
	}
}
