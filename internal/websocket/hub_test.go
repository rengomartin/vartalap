package websocket

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"vartalap/internal/models"
	"vartalap/internal/services"
)

type stubRenderer struct{}

func (stubRenderer) RenderMessage(models.Message, string) ([]byte, error) {
	return []byte("message"), nil
}
func (stubRenderer) RenderPresence(int) ([]byte, error)          { return []byte("presence"), nil }
func (stubRenderer) RenderNotice(string, string) ([]byte, error) { return []byte("notice"), nil }

type stubStore struct{}

func (stubStore) CreateMessage(context.Context, *models.Message) error { return nil }
func (stubStore) GetUser(_ context.Context, id string) (models.User, bool, error) {
	return models.User{DeviceID: id, DisplayName: "Stub"}, true, nil
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition was not met before timeout")
}

func upgradeServer(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		conn.ReadMessage()
	}))
	t.Cleanup(server.Close)
	return server
}

func dialTestConn(t *testing.T, server *httptest.Server) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/socket", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func testClient(hub *Hub, conn *websocket.Conn, deviceID string) *Client {
	return NewClient(ClientConfig{
		Hub:             hub,
		Conn:            conn,
		DeviceID:        deviceID,
		Renderer:        stubRenderer{},
		Store:           stubStore{},
		Limiter:         services.NewLimiter(100, 100),
		Validate:        func(raw string, _ int) (string, error) { return raw, nil },
		MaxMessageBytes: 1024,
		MaxMessageRunes: 100,
	})
}

func TestHubTracksDistinctDevices(t *testing.T) {
	hub := NewHub(stubRenderer{})
	go hub.Run()
	defer hub.Close()

	server := upgradeServer(t)
	clientOne := testClient(hub, dialTestConn(t, server), "device-1")
	clientTwo := testClient(hub, dialTestConn(t, server), "device-1")
	clientThree := testClient(hub, dialTestConn(t, server), "device-2")

	hub.Register(clientOne)
	hub.Register(clientTwo)
	hub.Register(clientThree)
	waitFor(t, func() bool { return hub.Online() == 2 && hub.Connections() == 3 })

	hub.Unregister(clientOne)
	waitFor(t, func() bool { return hub.Online() == 2 && hub.Connections() == 2 })
	hub.Unregister(clientTwo)
	waitFor(t, func() bool { return hub.Online() == 1 && hub.Connections() == 1 })
	hub.Unregister(clientThree)
	waitFor(t, func() bool { return hub.Online() == 0 && hub.Connections() == 0 })
}

func TestSlowClientIsDisconnectedInsteadOfBlockingBroadcast(t *testing.T) {
	hub := NewHub(stubRenderer{})
	go hub.Run()
	defer hub.Close()

	client := testClient(hub, dialTestConn(t, upgradeServer(t)), "device-1")
	hub.Register(client)
	waitFor(t, func() bool { return hub.Connections() == 1 })

	for i := 0; i < sendQueueSize+5; i++ {
		select {
		case client.send <- []byte("queued"):
		default:
		}
	}

	hub.Broadcast(models.Message{ID: 1, DeviceID: "device-2", SenderName: "Other", Content: "hi"})
	waitFor(t, func() bool { return hub.Connections() == 0 && hub.Online() == 0 })
}
