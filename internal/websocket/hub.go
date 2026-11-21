// Package websocket implements the in-memory connection registry and broadcaster.
package websocket

import (
	"sync"

	"vartalap/internal/models"
)

// Renderer converts domain events into HTML fragments for a viewer.
type Renderer interface {
	RenderMessage(message models.Message, viewerID string) ([]byte, error)
	RenderPresence(count int) ([]byte, error)
	RenderNotice(level, text string) ([]byte, error)
}

// Hub fans messages out to connected clients and tracks presence.
type Hub struct {
	mu       sync.Mutex
	clients  map[*Client]struct{}
	devices  map[string]int
	renderer Renderer

	register   chan *Client
	unregister chan *Client
	messages   chan models.Message
	done       chan struct{}
	closeOnce  sync.Once
}

// NewHub creates a hub. Call Run in a goroutine before registering clients.
func NewHub(renderer Renderer) *Hub {
	return &Hub{
		clients:    make(map[*Client]struct{}),
		devices:    make(map[string]int),
		renderer:   renderer,
		register:   make(chan *Client, 128),
		unregister: make(chan *Client, 128),
		messages:   make(chan models.Message, 256),
		done:       make(chan struct{}),
	}
}

// Run processes registrations, disconnections and broadcasts serially.
func (h *Hub) Run() {
	for {
		select {
		case client := <-h.register:
			h.add(client)
		case client := <-h.unregister:
			h.remove(client)
		case message := <-h.messages:
			h.broadcast(message)
		case <-h.done:
			return
		}
	}
}

// Register adds a client to the hub.
func (h *Hub) Register(client *Client) {
	select {
	case h.register <- client:
	case <-h.done:
	}
}

// Unregister removes a client from the hub.
func (h *Hub) Unregister(client *Client) {
	select {
	case h.unregister <- client:
	case <-h.done:
	}
}

// Broadcast queues a message for all connected clients.
func (h *Hub) Broadcast(message models.Message) {
	select {
	case h.messages <- message:
	case <-h.done:
	}
}

// Online returns the number of distinct devices with at least one connection.
func (h *Hub) Online() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.devices)
}

// Connections returns the total number of open sockets (used by tests and diagnostics).
func (h *Hub) Connections() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
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
		h.devices = make(map[string]int)
		h.mu.Unlock()
	})
}

func (h *Hub) add(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.clients[client]; exists {
		return
	}
	h.clients[client] = struct{}{}
	h.devices[client.deviceID]++
	h.sendPresenceLocked(len(h.devices))
}

func (h *Hub) remove(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.removeLocked(client)
}

func (h *Hub) removeLocked(client *Client) {
	if _, exists := h.clients[client]; !exists {
		return
	}
	delete(h.clients, client)
	client.close()
	if h.devices[client.deviceID] > 0 {
		h.devices[client.deviceID]--
		if h.devices[client.deviceID] == 0 {
			delete(h.devices, client.deviceID)
		}
	}
	h.sendPresenceLocked(len(h.devices))
}

func (h *Hub) broadcast(message models.Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var slow []*Client
	for client := range h.clients {
		html, err := h.renderer.RenderMessage(message, client.deviceID)
		if err != nil {
			continue
		}
		select {
		case client.send <- html:
		default:
			// A slow client must never block delivery to everyone else.
			slow = append(slow, client)
		}
	}
	for _, client := range slow {
		h.removeLocked(client)
	}
}

func (h *Hub) sendPresenceLocked(count int) {
	html, err := h.renderer.RenderPresence(count)
	if err != nil {
		return
	}
	for client := range h.clients {
		select {
		case client.send <- html:
		default:
			client.close()
		}
	}
}
