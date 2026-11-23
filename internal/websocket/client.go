package websocket

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait     = 10 * time.Second
	pongWait      = 60 * time.Second
	pingPeriod    = 50 * time.Second
	sendQueueSize = 512
	maxAckBatch   = 100
)

// RateLimiter slows spam from a single account.
type RateLimiter interface {
	Allow(key string, now time.Time) bool
}

// ClientConfig contains everything a client needs to run.
type ClientConfig struct {
	Hub             *Hub
	Conn            *websocket.Conn
	UserID          string
	DeviceID        string
	Store           AckStore
	Limiter         RateLimiter
	MaxMessageBytes int64
	Logger          *log.Logger
}

// Client is one browser tab's authenticated WebSocket connection.
type Client struct {
	hub      *Hub
	conn     *websocket.Conn
	UserID   string
	DeviceID string
	store    AckStore
	limiter  RateLimiter
	logger   *log.Logger
	maxBytes int64

	send      chan []byte
	done      chan struct{}
	closeOnce sync.Once
	typing    map[string]time.Time
}

// NewClient wraps an upgraded WebSocket connection.
func NewClient(config ClientConfig) *Client {
	return &Client{
		hub:      config.Hub,
		conn:     config.Conn,
		UserID:   config.UserID,
		DeviceID: config.DeviceID,
		store:    config.Store,
		limiter:  config.Limiter,
		logger:   config.Logger,
		maxBytes: config.MaxMessageBytes,
		send:     make(chan []byte, sendQueueSize),
		done:     make(chan struct{}),
		typing:   make(map[string]time.Time),
	}
}

// Enqueue adds a pre-marshalled event to this client's outbound queue. It may
// be called before Start, which is how pending messages are delivered.
func (c *Client) Enqueue(payload []byte) {
	c.enqueue(payload)
}

// EnqueueJSON marshals and enqueues an event.
func (c *Client) EnqueueJSON(event any) {
	payload, err := json.Marshal(event)
	if err != nil {
		return
	}
	c.enqueue(payload)
}

func (c *Client) enqueue(payload []byte) {
	select {
	case c.send <- payload:
	default:
		// A slow client must never block routing for everyone else.
		c.close()
	}
}

// Start registers the client, then runs the read loop until disconnect.
func (c *Client) Start() {
	c.hub.Register(c)
	go c.writePump()
	c.readPump()
	c.hub.Unregister(c)
}

func (c *Client) close() {
	c.closeOnce.Do(func() {
		close(c.done)
		_ = c.conn.Close()
	})
}

type inboundEvent struct {
	Type           string   `json:"type"`
	ConversationID string   `json:"conversationId"`
	State          string   `json:"state"`
	MessageIDs     []string `json:"messageIds"`
}

func (c *Client) readPump() {
	c.conn.SetReadLimit(c.maxBytes)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
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
	var event inboundEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		c.notify("That request could not be read.")
		return
	}
	switch event.Type {
	case "ping":
		c.EnqueueJSON(map[string]string{"type": "pong"})
	case "ack":
		c.handleAck(event.MessageIDs)
	case "typing":
		c.handleTyping(event)
	default:
		c.notify("Unsupported real-time event.")
	}
}

func (c *Client) handleAck(messageIDs []string) {
	if len(messageIDs) == 0 || len(messageIDs) > maxAckBatch {
		c.notify("Invalid acknowledgement batch.")
		return
	}
	if !c.limiter.Allow("ack:"+c.UserID, time.Now()) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	results, err := c.store.AckMessages(ctx, c.DeviceID, messageIDs)
	if err != nil {
		if c.logger != nil {
			c.logger.Printf("ack messages: %v", err)
		}
		c.notify("Acknowledgement could not be saved.")
		return
	}
	for _, result := range results {
		c.hub.SendToUserJSON(result.SenderUserID, map[string]any{
			"type":      "delivery",
			"messageId": result.MessageID,
			"state":     "delivered",
		})
	}
}

func (c *Client) handleTyping(event inboundEvent) {
	if event.State != "start" && event.State != "stop" {
		return
	}
	if event.ConversationID == "" || !c.limiter.Allow("typing:"+c.UserID, time.Now()) {
		return
	}
	// Throttle per conversation to avoid a typing-indicator flood.
	if last, ok := c.typing[event.ConversationID]; ok && time.Since(last) < 1500*time.Millisecond {
		return
	}
	c.typing[event.ConversationID] = time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	member, err := c.store.IsConversationMember(ctx, event.ConversationID, c.UserID)
	if err != nil || !member {
		return
	}
	peer, found, err := c.store.ConversationPeer(ctx, event.ConversationID, c.UserID)
	if err != nil || !found {
		return
	}
	c.hub.SendToUserJSON(peer, map[string]any{
		"type":           "typing",
		"conversationId": event.ConversationID,
		"userId":         c.UserID,
		"state":          event.State,
	})
}

func (c *Client) notify(message string) {
	c.EnqueueJSON(map[string]string{"type": "error", "message": message})
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()
	for {
		select {
		case payload, ok := <-c.send:
			if !ok {
				return
			}
			if err := c.conn.SetWriteDeadline(time.Now().Add(writeWait)); err != nil {
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
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

func presenceEvent(userID string, online bool) []byte {
	payload, _ := json.Marshal(map[string]any{
		"type":   "presence",
		"userId": userID,
		"online": online,
	})
	return payload
}
