// Package models contains the value types shared across layers.
package models

import "time"

// User is a Vartalap account. Accounts are anonymous: there is no email,
// phone number or password. The cryptographic identity lives on the user's
// device; the server only stores public keys.
type User struct {
	ID              string
	Username        string
	DisplayName     string
	AvatarUpdatedAt int64
	Discoverable    bool
	PrimaryDeviceID string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Summary is the public, non-sensitive view of a user.
type Summary struct {
	ID              string `json:"id"`
	Username        string `json:"username"`
	DisplayName     string `json:"displayName"`
	AvatarUpdatedAt int64  `json:"avatarUpdatedAt"`
	Discoverable    bool   `json:"discoverable"`
}

// Summary converts a full user record into its public projection.
func (u User) Summary() Summary {
	return Summary{
		ID:              u.ID,
		Username:        u.Username,
		DisplayName:     u.DisplayName,
		AvatarUpdatedAt: u.AvatarUpdatedAt,
		Discoverable:    u.Discoverable,
	}
}

// Device holds the public half of a device's cryptographic identity.
type Device struct {
	ID           string
	UserID       string
	Name         string
	EncPublicKey string
	SigPublicKey string
	CreatedAt    time.Time
	LastSeenAt   time.Time
	RevokedAt    *time.Time
}

// KeyBundle is the public key material a sender needs to encrypt a message for
// a recipient. It contains no private key material.
type KeyBundle struct {
	UserID          string `json:"userId"`
	Username        string `json:"username"`
	DisplayName     string `json:"displayName"`
	AvatarUpdatedAt int64  `json:"avatarUpdatedAt"`
	DeviceID        string `json:"deviceId"`
	EncPublicKey    string `json:"encPublicKey"`
	SigPublicKey    string `json:"sigPublicKey"`
}

// Conversation is a private 1:1 conversation.
type Conversation struct {
	ID        string
	UserA     string
	UserB     string
	CreatedAt time.Time
	Peer      Summary
}

// Envelope is the only message shape the server ever sees. Ciphertext,
// ephemeral key, IV and signature are all produced by the client.
type Envelope struct {
	ID                 string `json:"id"`
	ConversationID     string `json:"conversationId"`
	SenderUserID       string `json:"senderUserId"`
	SenderDeviceID     string `json:"senderDeviceId"`
	RecipientUserID    string `json:"recipientUserId"`
	Type               string `json:"type"`
	EphemeralPublicKey string `json:"ephemeralPublicKey"`
	IV                 string `json:"iv"`
	Ciphertext         string `json:"ciphertext"`
	Signature          string `json:"signature"`
	SenderSigningKey   string `json:"senderSigningKey"`
	ClientCreatedAt    int64  `json:"clientCreatedAt"`
	ServerCreatedAt    int64  `json:"serverCreatedAt,omitempty"`
	State              string `json:"state,omitempty"`
}

// MessageEvent is delivered over the API and WebSocket with the sender's
// public profile attached so a recipient can render a new conversation.
type MessageEvent struct {
	Envelope Envelope `json:"envelope"`
	Sender   Summary  `json:"sender"`
}

// Attachment describes stored ciphertext only. No filename or MIME type is
// known to the server.
type Attachment struct {
	ID             string
	ConversationID string
	UploaderUserID string
	CiphertextSize int64
	CreatedAt      time.Time
	ExpiresAt      time.Time
}

// IdentityBackup is the client-encrypted recovery blob.
type IdentityBackup struct {
	UserID        string
	Ciphertext    string
	IV            string
	KDFSalt       string
	KDFIterations int
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// PageData is the view model for a rendered HTML page.
type PageData struct {
	Title   string
	CSRF    string
	User    *User
	Profile *Summary
	IsSelf  bool
	View    string
	Notice  string
}

// DirectoryPage is the paginated public user directory.
type DirectoryPage struct {
	Users      []Summary
	NextCursor string
	Query      string
}
