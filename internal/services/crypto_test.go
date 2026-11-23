package services

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"vartalap/internal/models"
)

func newKeyPair(t *testing.T) (*ecdsa.PrivateKey, string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	raw := elliptic.Marshal(elliptic.P256(), key.PublicKey.X, key.PublicKey.Y)
	return key, base64.StdEncoding.EncodeToString(raw)
}

func signRaw(t *testing.T, key *ecdsa.PrivateKey, message string) string {
	t.Helper()
	digest := sha256.Sum256([]byte(message))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	raw := make([]byte, SignatureBytes)
	r.FillBytes(raw[:32])
	s.FillBytes(raw[32:])
	return base64.StdEncoding.EncodeToString(raw)
}

func TestVerifySignatureRoundTrip(t *testing.T) {
	key, publicKey := newKeyPair(t)
	message := CanonicalRegistration("device", "aap", "Aap Ji", "enc", "sig", 123)
	signature := signRaw(t, key, message)
	if err := VerifySignature(publicKey, signature, message); err != nil {
		t.Fatalf("VerifySignature: %v", err)
	}
	if err := VerifySignature(publicKey, signature, message+"x"); err == nil {
		t.Fatal("tampered message should fail verification")
	}
	_, otherPublicKey := newKeyPair(t)
	if err := VerifySignature(otherPublicKey, signature, message); err == nil {
		t.Fatal("a different key should fail verification")
	}
}

func TestCanonicalTranscripts(t *testing.T) {
	if got := CanonicalRegistration("d", "u", "n", "e", "s", 7); got !=
		"vartalap:register:v1\nd\nu\nn\ne\ns\n7" {
		t.Fatalf("registration transcript = %q", got)
	}
	if got := CanonicalRecovery("u", "d", "nonce", 8); got !=
		"vartalap:recover:v1\nu\nd\nnonce\n8" {
		t.Fatalf("recovery transcript = %q", got)
	}
	envelope := models.Envelope{
		ID: "id", ConversationID: "c", SenderUserID: "a", SenderDeviceID: "ad",
		RecipientUserID: "b", Type: "text", EphemeralPublicKey: "e", IV: "i", Ciphertext: "c",
	}
	if got := CanonicalMessage(envelope); got != "vartalap:message:v1\nid\nc\na\nad\nb\ntext\ne\ni\nc" {
		t.Fatalf("message transcript = %q", got)
	}
}

func TestFingerprintIsStableAndGrouped(t *testing.T) {
	first := Fingerprint("enc", "sig")
	second := Fingerprint("enc", "sig")
	if first != second {
		t.Fatal("fingerprint must be deterministic")
	}
	if len(strings.Fields(first)) != 8 {
		t.Fatalf("fingerprint should have 8 groups, got %q", first)
	}
	if first == Fingerprint("enc", "other") {
		t.Fatal("different keys must produce different fingerprints")
	}
}

func TestValidateEnvelope(t *testing.T) {
	key, publicKey := newKeyPair(t)
	_, ephemeral := newKeyPair(t)
	now := time.Now().UTC().UnixMilli()
	envelope := models.Envelope{
		ID:                 mustUUID(t),
		ConversationID:     mustUUID(t),
		SenderUserID:       mustUUID(t),
		SenderDeviceID:     mustUUID(t),
		RecipientUserID:    mustUUID(t),
		Type:               "text",
		EphemeralPublicKey: ephemeral,
		IV:                 base64.StdEncoding.EncodeToString(make([]byte, 12)),
		Ciphertext:         base64.StdEncoding.EncodeToString([]byte("ciphertext")),
		SenderSigningKey:   publicKey,
		ClientCreatedAt:    now,
	}
	envelope.Signature = signRaw(t, key, CanonicalMessage(envelope))
	if err := ValidateEnvelope(envelope); err != nil {
		t.Fatalf("valid envelope rejected: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*models.Envelope)
	}{
		{"bad id", func(e *models.Envelope) { e.ID = "nope" }},
		{"self recipient", func(e *models.Envelope) { e.RecipientUserID = e.SenderUserID }},
		{"bad type", func(e *models.Envelope) { e.Type = "video" }},
		{"bad ephemeral", func(e *models.Envelope) { e.EphemeralPublicKey = "AAAA" }},
		{"bad iv", func(e *models.Envelope) { e.IV = base64.StdEncoding.EncodeToString([]byte("short")) }},
		{"empty ciphertext", func(e *models.Envelope) { e.Ciphertext = "" }},
		{"future timestamp", func(e *models.Envelope) {
			e.ClientCreatedAt = time.Now().Add(time.Hour).UnixMilli()
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			copy := envelope
			test.mutate(&copy)
			if err := ValidateEnvelope(copy); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestDecodeBase64RejectsOversize(t *testing.T) {
	value := base64.StdEncoding.EncodeToString(make([]byte, 128))
	if _, err := DecodeBase64(value, 32); err == nil {
		t.Fatal("expected oversize error")
	}
	if _, err := DecodeBase64("!!!!", 32); err == nil {
		t.Fatal("expected invalid base64 error")
	}
}

func TestValidateUsername(t *testing.T) {
	valid := []string{"abc", "neel_22", "a_b_c", "user123"}
	for _, value := range valid {
		if _, err := ValidateUsername(value); err != nil {
			t.Fatalf("ValidateUsername(%q): %v", value, err)
		}
	}
	invalid := []string{"", "ab", "with space", "_start", "end_", "two__underscore", "admin", "has-dash"}
	for _, value := range invalid {
		if _, err := ValidateUsername(value); err == nil {
			t.Fatalf("ValidateUsername(%q) should fail", value)
		}
	}
}

func mustUUID(t *testing.T) string {
	t.Helper()
	id, err := NewUUID()
	if err != nil {
		t.Fatalf("NewUUID: %v", err)
	}
	return id
}
