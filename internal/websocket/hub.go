// Package websocket implements the authenticated real-time hub. It routes
// opaque ciphertext envelopes, presence and typing indicators. It never sees
// plaintext.
package websocket

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"vartalap/internal/repository"
)

// PresenceDirectory supplies the conversation partners that should receive
// presence changes. Keeping it an interface avoids coupling the hub to SQL.
type PresenceDirectory interface {
	ConversationPartnerIDs(ctx context.Context, userID string) ([]string, error)
}

// Hub tracks connected devices and routes events between users.
type Hub struct {
	mu        sync.Mutex
	clients   map[*Client]struct{}
	users     map[string]map[*Client]struct{}
	dir       PresenceDirectory
	done      chan struct{}
	closeOnce sync.Once
}

// NewHub creates a hub. Call Run is not required; registration is synchronous.
func NewHub(dir PresenceDirectory) *Hub {
	return &Hub{
		clients: make(map[*Client]struct{}),
		users:   make(map[string]map[*Client]struct{}),
		dir:     dir,
		done:    make(chan struct{}),
	}
}

// Register adds a client and emits presence transitions.
func (h *Hub) Register(client *Client) {
	select {
	case <-h.done:
		client.close()
		return
	default:
	}

	var partners []string
	if h.dir != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		partners, _ = h.dir.ConversationPartnerIDs(ctx, client.UserID)
		cancel()
	}

	h.mu.Lock()
	if _, exists := h.clients[client]; exists {
		h.mu.Unlock()
		return
	}
	firstConnection := len(h.users[client.UserID]) == 0
	h.clients[client] = struct{}{}
	if h.users[client.UserID] == nil {
		h.users[client.UserID] = make(map[*Client]struct{})
	}
	h.users[client.UserID][client] = struct{}{}

	// Send the new client the current presence of its partners.
	for _, partner := range partners {
		if h.isOnlineLocked(partner) {
			client.enqueue(presenceEvent(partner, true))
		}
	}
	// Tell partners this user is online, but only on the first connection.
	if firstConnection {
		for _, partner := range partners {
			h.sendToUserLocked(partner, presenceEvent(client.UserID, true))
		}
	}
	h.mu.Unlock()
}

// Unregister removes a client and emits an offline event when the user's last
// connection closes.
func (h *Hub) Unregister(client *Client) {
	select {
	case <-h.done:
		client.close()
		return
	default:
	}

	var partners []string
	if h.dir != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		partners, _ = h.dir.ConversationPartnerIDs(ctx, client.UserID)
		cancel()
	}

	h.mu.Lock()
	h.removeLocked(client, partners)
	h.mu.Unlock()
}

func (h *Hub) removeLocked(client *Client, partners []string) {
	if _, exists := h.clients[client]; !exists {
		return
	}
	delete(h.clients, client)
	client.close()
	remaining := h.users[client.UserID]
	delete(remaining, client)
	if len(remaining) == 0 {
		delete(h.users, client.UserID)
		for _, partner := range partners {
			h.sendToUserLocked(partner, presenceEvent(client.UserID, false))
		}
	}
}

// SendToUser enqueues a pre-marshalled event for every connection of a user.
func (h *Hub) SendToUser(userID string, payload []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.sendToUserLocked(userID, payload)
}

// SendToUserJSON marshals and sends an event.
func (h *Hub) SendToUserJSON(userID string, event any) {
	payload, err := json.Marshal(event)
	if err != nil {
		return
	}
	h.SendToUser(userID, payload)
}

// SendToDevice sends an event only to connections of a specific device.
func (h *Hub) SendToDevice(deviceID string, event any) {
	payload, err := json.Marshal(event)
	if err != nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for client := range h.clients {
		if client.DeviceID == deviceID {
			client.enqueue(payload)
		}
	}
}

// IsOnline reports whether a user has at least one open socket.
func (h *Hub) IsOnline(userID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.isOnlineLocked(userID)
}

func (h *Hub) isOnlineLocked(userID string) bool {
	return len(h.users[userID]) > 0
}

// OnlinePartners returns which of a user's conversation partners are online.
func (h *Hub) OnlinePartners(ctx context.Context, userID string) []string {
	if h.dir == nil {
		return nil
	}
	partners, err := h.dir.ConversationPartnerIDs(ctx, userID)
	if err != nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	online := make([]string, 0, len(partners))
	for _, partner := range partners {
		if h.isOnlineLocked(partner) {
			online = append(online, partner)
		}
	}
	return online
}

// Online returns the number of distinct connected users.
func (h *Hub) Online() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.users)
}

// Connections returns the total number of open sockets.
func (h *Hub) Connections() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

// UserConnections returns the number of open sockets for one user.
func (h *Hub) UserConnections(userID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.users[userID])
}

// Close stops the hub and closes every client connection.
func (h *Hub) Close() {
	h.closeOnce.Do(func() {
		close(h.done)
		h.mu.Lock()
		for client := range h.clients {
			client.close()
		}
		h.clients = make(map[*Client]struct{})
		h.users = make(map[string]map[*Client]struct{})
		h.mu.Unlock()
	})
}

func (h *Hub) sendToUserLocked(userID string, payload []byte) {
	for client := range h.users[userID] {
		client.enqueue(payload)
	}
}

// AckStore is the persistence surface used by the WebSocket client.
type AckStore interface {
	IsConversationMember(ctx context.Context, conversationID, userID string) (bool, error)
	ConversationPeer(ctx context.Context, conversationID, userID string) (string, bool, error)
	AckMessages(ctx context.Context, deviceID string, messageIDs []string) ([]repository.AckResult, error)
}
