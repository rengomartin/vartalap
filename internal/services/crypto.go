package services

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"

	"vartalap/internal/models"
)

const (
	// RegistrationDomain, RecoveryDomain and MessageDomain are part of the
	// signed transcript. The browser signs the exact same newline-separated
	// strings, so changing one side without the other breaks verification.
	RegistrationDomain = "vartalap:register:v1"
	RecoveryDomain     = "vartalap:recover:v1"
	MessageDomain      = "vartalap:message:v1"

	PublicKeyBytes  = 65 // uncompressed P-256 point
	SignatureBytes  = 64 // raw r || s
	MaxEnvelopeSize = 64 * 1024
)

// DecodeBase64 decodes standard padded base64, rejecting oversized input.
func DecodeBase64(value string, maxBytes int) ([]byte, error) {
	if len(value) > (maxBytes/3+1)*4+4 {
		return nil, fmt.Errorf("encoded value is too large")
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, fmt.Errorf("invalid base64")
	}
	if len(decoded) > maxBytes {
		return nil, fmt.Errorf("decoded value is too large")
	}
	return decoded, nil
}

// ParsePublicKey decodes and validates an uncompressed P-256 public key.
func ParsePublicKey(encoded string) (*ecdsa.PublicKey, error) {
	raw, err := DecodeBase64(encoded, PublicKeyBytes)
	if err != nil {
		return nil, err
	}
	if len(raw) != PublicKeyBytes || raw[0] != 0x04 {
		return nil, fmt.Errorf("invalid public key")
	}
	x, y := elliptic.Unmarshal(elliptic.P256(), raw)
	if x == nil || y == nil {
		return nil, fmt.Errorf("public key is not on the P-256 curve")
	}
	return &ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}, nil
}

// ValidatePublicKey checks the encoding without retaining the key.
func ValidatePublicKey(encoded string) error {
	_, err := ParsePublicKey(encoded)
	return err
}

// ParseSignature decodes a raw IEEE-P1363 ECDSA signature (r || s).
func ParseSignature(encoded string) (*big.Int, *big.Int, error) {
	raw, err := DecodeBase64(encoded, SignatureBytes)
	if err != nil {
		return nil, nil, err
	}
	if len(raw) != SignatureBytes {
		return nil, nil, fmt.Errorf("invalid signature length")
	}
	r := new(big.Int).SetBytes(raw[:32])
	s := new(big.Int).SetBytes(raw[32:])
	if r.Sign() == 0 || s.Sign() == 0 {
		return nil, nil, fmt.Errorf("invalid signature")
	}
	return r, s, nil
}

// VerifySignature verifies a WebCrypto ECDSA P-256 SHA-256 signature over the
// UTF-8 bytes of message.
func VerifySignature(publicKeyBase64, signatureBase64, message string) error {
	publicKey, err := ParsePublicKey(publicKeyBase64)
	if err != nil {
		return err
	}
	r, s, err := ParseSignature(signatureBase64)
	if err != nil {
		return err
	}
	digest := sha256.Sum256([]byte(message))
	if !ecdsa.Verify(publicKey, digest[:], r, s) {
		return fmt.Errorf("signature verification failed")
	}
	return nil
}

// CanonicalRegistration is the transcript signed during account creation.
func CanonicalRegistration(deviceID, username, displayName, encPublicKey, sigPublicKey string, timestamp int64) string {
	return strings.Join([]string{
		RegistrationDomain, deviceID, username, displayName,
		encPublicKey, sigPublicKey, strconv.FormatInt(timestamp, 10),
	}, "\n")
}

// CanonicalRecovery is the transcript signed to prove ownership of a restored
// identity. The recovery phrase itself is never sent to the server.
func CanonicalRecovery(userID, deviceID, nonce string, timestamp int64) string {
	return strings.Join([]string{
		RecoveryDomain, userID, deviceID, nonce, strconv.FormatInt(timestamp, 10),
	}, "\n")
}

// CanonicalMessage is the transcript signed by the sender for every envelope.
func CanonicalMessage(envelope models.Envelope) string {
	return strings.Join([]string{
		MessageDomain,
		envelope.ID,
		envelope.ConversationID,
		envelope.SenderUserID,
		envelope.SenderDeviceID,
		envelope.RecipientUserID,
		envelope.Type,
		envelope.EphemeralPublicKey,
		envelope.IV,
		envelope.Ciphertext,
	}, "\n")
}

// Fingerprint derives a short, human-comparable identity fingerprint from the
// two public keys. It is safe to display and log.
func Fingerprint(encPublicKey, sigPublicKey string) string {
	digest := sha256.Sum256([]byte("vartalap:fingerprint:v1|" + encPublicKey + "|" + sigPublicKey))
	encoded := hex.EncodeToString(digest[:16])
	groups := make([]string, 0, 8)
	for i := 0; i < len(encoded); i += 4 {
		groups = append(groups, encoded[i:i+4])
	}
	return strings.Join(groups, " ")
}

// ValidateEnvelope checks the structural integrity of a client envelope. It
// cannot and does not inspect plaintext.
func ValidateEnvelope(envelope models.Envelope) error {
	if !IsValidUUID(envelope.ID) {
		return fmt.Errorf("invalid message id")
	}
	if !IsValidUUID(envelope.ConversationID) {
		return fmt.Errorf("invalid conversation id")
	}
	if !IsValidUUID(envelope.SenderUserID) || !IsValidUUID(envelope.RecipientUserID) {
		return fmt.Errorf("invalid participant id")
	}
	if !IsValidUUID(envelope.SenderDeviceID) {
		return fmt.Errorf("invalid device id")
	}
	if envelope.SenderUserID == envelope.RecipientUserID {
		return fmt.Errorf("cannot send a message to yourself")
	}
	switch envelope.Type {
	case "text", "attachment":
	default:
		return fmt.Errorf("unsupported message type")
	}
	if err := ValidatePublicKey(envelope.EphemeralPublicKey); err != nil {
		return fmt.Errorf("invalid ephemeral key: %w", err)
	}
	if err := ValidatePublicKey(envelope.SenderSigningKey); err != nil {
		return fmt.Errorf("invalid sender signing key: %w", err)
	}
	iv, err := DecodeBase64(envelope.IV, 16)
	if err != nil || len(iv) != 12 {
		return fmt.Errorf("invalid iv")
	}
	ciphertext, err := DecodeBase64(envelope.Ciphertext, MaxEnvelopeSize)
	if err != nil || len(ciphertext) == 0 {
		return fmt.Errorf("invalid ciphertext")
	}
	if _, _, err := ParseSignature(envelope.Signature); err != nil {
		return fmt.Errorf("invalid signature encoding")
	}
	if envelope.ClientCreatedAt <= 0 {
		return fmt.Errorf("invalid client timestamp")
	}
	// Reject absurd future timestamps, but allow old queued messages.
	if envelope.ClientCreatedAt > time.Now().Add(10*time.Minute).UnixMilli() {
		return fmt.Errorf("client timestamp is in the future")
	}
	return nil
}
