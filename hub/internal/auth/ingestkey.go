package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
)

// IngestKeyPrefix is the human-visible prefix on every raw ingest key. The
// stored Prefix (first 12 chars) always begins with it, so operators can
// recognise a leaked key at a glance.
const IngestKeyPrefix = "avuruk_"

// NewIngestKey returns (raw, prefix, hash). The raw key is shown to the admin
// exactly once at creation; only hash is persisted. prefix (the first 12 chars)
// is stored in clear for UI identification.
func NewIngestKey() (raw, prefix, hash string) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand failure is unrecoverable
	}
	raw = IngestKeyPrefix + base64.RawURLEncoding.EncodeToString(b)
	return raw, raw[:12], HashIngestKey(raw)
}

// HashIngestKey is the storage/lookup hash (hex sha256). Lookups are by exact
// hash — no per-key salt (keys are high-entropy randoms, not passwords, so the
// dictionary/rainbow concerns that motivate bcrypt for passwords do not apply).
func HashIngestKey(raw string) string {
	h := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(h[:])
}
