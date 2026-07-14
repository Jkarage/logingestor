package sourcebus

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// KeyScheme is the prefix every source ingest key carries. It lets the
// ingestion path cheaply reject non-source tokens before any DB lookup.
const KeyScheme = "ls_src_live_"

// keyRandomBytes is the entropy of the random portion of the key (256-bit).
const keyRandomBytes = 32

// GenerateKey mints a new ingest key. It returns the raw key (shown to the
// caller exactly once), the SHA-256 hash to persist, and a display prefix.
// The raw key is never stored.
func GenerateKey() (raw string, keyHash string, keyPrefix string, err error) {
	buf := make([]byte, keyRandomBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", "", "", fmt.Errorf("generate key: %w", err)
	}

	randomHex := hex.EncodeToString(buf)
	raw = KeyScheme + randomHex
	keyHash = HashKey(raw)

	// Display prefix: scheme + first 6 hex of the random part. Enough to
	// recognize a key in the UI without revealing it; lookup is by hash.
	keyPrefix = KeyScheme + randomHex[:6]

	return raw, keyHash, keyPrefix, nil
}

// HashKey returns the hex-encoded SHA-256 of a raw key. Used both when minting
// keys and when authenticating an inbound ingest request.
func HashKey(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// HasKeyScheme reports whether raw looks like a source ingest key.
func HasKeyScheme(raw string) bool {
	return strings.HasPrefix(raw, KeyScheme)
}
