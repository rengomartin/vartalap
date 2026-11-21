package services

import (
	"crypto/sha256"
	"encoding/hex"
)

// SenderKey derives a short, stable pseudonym from a device ID. It is safe to
// put in markup: it groups consecutive messages from the same sender without
// exposing the underlying identity token/device ID.
func SenderKey(deviceID string) string {
	sum := sha256.Sum256([]byte("vartalap-sender:" + deviceID))
	return hex.EncodeToString(sum[:6])
}
