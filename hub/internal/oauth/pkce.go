package oauth

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
)

// ChallengeMethodS256 is the only PKCE method this server accepts, and the only
// one its metadata advertises.
//
// `plain` is deliberately absent everywhere. Advertising it is the downgrade
// vector — a client that would have used S256 can be talked into `plain` by a
// metadata document it trusts — and no client this server exists for needs it.
const ChallengeMethodS256 = "S256"

// VerifyPKCE reports whether verifier matches the stored S256 challenge.
//
// Constant-time comparison: the challenge is not a secret, but the verifier is,
// and a byte-by-byte early exit leaks how much of a guess was right.
func VerifyPKCE(challenge, verifier string) bool {
	if challenge == "" || !ValidVerifier(verifier) {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtle.ConstantTimeCompare([]byte(want), []byte(challenge)) == 1
}

// ValidVerifier enforces RFC 7636's length bounds. A short verifier is
// brute-forceable, which would undo the whole point of PKCE.
func ValidVerifier(v string) bool {
	return len(v) >= 43 && len(v) <= 128
}

// ValidChallenge rejects an obviously malformed S256 challenge — 32 bytes,
// base64url, no padding.
func ValidChallenge(c string) bool {
	if len(c) != 43 {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(c)
	return err == nil
}
