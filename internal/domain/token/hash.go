package token

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

// HashAPIKey creates the deterministic HMAC digest used for token identity lookups.
func HashAPIKey(rawAPIKey, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(rawAPIKey))
	return hex.EncodeToString(mac.Sum(nil))
}
