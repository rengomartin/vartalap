package websocket

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"vartalap/internal/models"
)

const (
	writeWait     = 10 * time.Second
	pongWait      = 60 * time.Second
	pingPeriod    = 50 * time.Second
	sendQueueSize = 32
)

// MessageStore is the persistence surface needed by a client.
type MessageStore interface {
	CreateMessage(context.Context, *models.Message) error
	GetUser(context.Context, string) (models.User, bool, error)
}

// RateLimiter slows spam from a single anonymous device.
type RateLimiter interface {
	Allow(key string, now time.Time) bool
}

// Validator normalises and validates raw message text.
type Validator func(raw string, maxRunes int) (string, error)

// ClientConfig contains everything a client needs to run.
type ClientConfig struct {
	Hub             *Hub
	Conn            *websocket.Conn
	DeviceID        string
	Store           MessageStore
	Limiter         RateLimiter
	Renderer        Renderer
	Validate        Validator
	MaxMessageBytes int64
	MaxMessageRunes int
	Logger          *log.Logger
}

// Client is one browser tab's WebSocket connection.
type Client struct {
	hub             *Hub
	conn            *websocket.Conn
	deviceID        string
	store           MessageStore
	limiter         RateLimiter
	renderer        Renderer
	validate        Validator
	maxMessageBytes int64
	maxMessageRunes int
	logger          *log.Logger

	send      chan []byte
	done      chan struct{}
	closeOnce sync.Once
}

// NewClient wraps an upgraded WebSocket connection.
func NewClient(config ClientConfig) *Client {
	return &Client{
		hub:             config.Hub,
		conn:            config.Conn,
		deviceID:        config.DeviceID,
		store:           config.Store,
		limiter:         config.Limiter,
		renderer:        config.Renderer,
		validate:        config.Validate,
		maxMessageBytes: config.MaxMessageBytes,
		maxMessageRunes: config.MaxMessageRunes,
		logger:          config.Logger,
		send:            make(chan []byte, sendQueueSize),
		done:            make(chan struct{}),
	}
}

// Start registers the client with the hub, then runs the read loop until disconnect.
func (c *Client) Start() {
	c.hub.Register(c)
	go c.writePump()
	c.readPump()
	c.hub.Unregister(c)
}

func (c *Client) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		c.conn.Close()
	})
}

// Notify sends a toast fragment to this client only.
func (c *Client) Notify(level, text string) {
	html, err := c.renderer.RenderNotice(level, text)
	if err != nil {
		return
	}
	select {
	case c.send <- html:
	default:
	}
}

func (c *Client) readPump() {
	c.conn.SetReadLimit(c.maxMessageBytes)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		_, payload, err := c.conn.ReadMessage()
		if err != nil {
			return
		}
		c.handlePayload(payload)
	}
}

func (c *Client) handlePayload(payload []byte) {
	var incoming struct {
		Content string `json:"content"`
		Nonce   string `json:"nonce"`
	}
	if err := json.Unmarshal(payload, &incoming); err != nil {
		c.Notify("error", "That message could not be read.")
		return
	}
	content, err := c.validate(incoming.Content, c.maxMessageRunes)
	if err != nil {
		c.Notify("error", err.Error())
		return
	}
	if !c.limiter.Allow(c.deviceID, time.Now()) {
		c.Notify("error", "Slow down a little before sending another message.")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	user, found, err := c.store.GetUser(ctx, c.deviceID)
	if err != nil || !found {
		c.Notify("error", "Your session has expired. Please reload the page.")
		return
	}

	message := models.Message{
		DeviceID:   c.deviceID,
		SenderName: user.DisplayName,
		Content:    content,
		Nonce:      cleanNonce(incoming.Nonce),
	}
	if err := c.store.CreateMessage(ctx, &message); err != nil {
		if c.logger != nil {
			c.logger.Printf("persist message: %v", err)
		}
		c.Notify("error", "Message could not be saved. Please try again.")
		return
	}
	c.hub.Broadcast(message)
}

// cleanNonce keeps client-supplied correlation tokens tiny and attribute-safe.
// html/template escapes the value, this is defence in depth.
func cleanNonce(raw string) string {
	if len(raw) == 0 || len(raw) > 64 {
		return ""
	}
	for _, r := range raw {
		allowed := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' ||
			r >= '0' && r <= '9' || r == '-' || r == '_'
		if !allowed {
			return ""
		}
	}
	return raw
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()
	for {
		select {
		case html, ok := <-c.send:
			if !ok {
				return
			}
			if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, html); err != nil {
				return
			}
		case <-ticker.C:
			if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				return
			}
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		case <-c.done:
			return
		}
	}
}
