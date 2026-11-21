// Package models contains the small value types shared across layers.
package models

import "time"

// User is the anonymous identity stored for a browser/device.
type User struct {
	DeviceID    string
	DisplayName string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Message is a single chat message.
type Message struct {
	ID         int64
	DeviceID   string
	SenderName string
	Content    string
	CreatedAt  time.Time

	// Mine is filled in when rendering for a particular viewer.
	Mine bool
	// Grouped is true when this message continues a run from the same sender.
	Grouped bool
	// Nonce is an optional client-generated token used to reconcile optimistic
	// messages with the broadcast copy. It is not persisted.
	Nonce string
}

// MarkViewer fills in the per-viewer Mine flag on a slice of messages so that
// page loads, history and live fragments all agree on message ownership.
func MarkViewer(messages []Message, viewerID string) {
	for i := range messages {
		messages[i].Mine = messages[i].DeviceID == viewerID
	}
}

// MessageList is the view model used by message templates.
type MessageList struct {
	Messages []Message
	ViewerID string
	HasMore  bool
	OldestID int64
}

// PageData is the view model for a rendered HTML page.
type PageData struct {
	Title       string
	DisplayName string
	DeviceKey   string
	Online      int
	Messages    []Message
	ViewerID    string
	HasMore     bool
	OldestID    int64
	MaxRunes    int
	Notice      string
}
