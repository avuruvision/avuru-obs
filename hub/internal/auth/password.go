package auth

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// productionBcryptCost 12 ≈ 250ms/hash on current hardware — login-path
// acceptable, brute-force hostile.
const productionBcryptCost = 12

// prodDummyHash is a fixed cost-12 hash, kept as a literal so a production
// process pays no bcrypt work at init. The plaintext is irrelevant — only the
// comparison cost matters.
const prodDummyHash = "$2a$12$crQm6wFj4lek4Cvpwn7/fuzoq6G9Edre2z3XEcAT9h4Ul3pxrFqQe"

// bcryptCost is productionBcryptCost everywhere except inside a `go test`
// binary, where it drops to bcrypt.MinCost.
//
// Under -race a cost-12 hash+compare pair costs ~5.5s, and the handler suites
// pay it on every admin bootstrap, login, and user create. That accumulated
// cost pushed `go test -race ./internal/api` past go test's 10-minute
// per-package timeout. testing.Testing() is true only in a binary built by
// `go test`, so no production build, flag, or env var can reach the cheap
// cost — which is the whole reason to gate on it rather than on config.
var bcryptCost = func() int {
	if testing.Testing() {
		return bcrypt.MinCost
	}
	return productionBcryptCost
}()

// dummyHash is compared when the user does not exist or has no password, so
// login latency does not reveal account existence.
var dummyHash = newDummyHash()

// newDummyHash keeps the dummy at bcryptCost. bcrypt reads the cost from the
// hash, not from the caller, so pinning the cost-12 literal in test binaries
// would leave the unknown-user and SSO-user paths at ~2.8s per call under
// -race even after bcryptCost drops.
func newDummyHash() []byte {
	if !testing.Testing() {
		return []byte(prodDummyHash)
	}
	h, err := bcrypt.GenerateFromPassword([]byte("avuru-obs/no-such-user"), bcryptCost)
	if err != nil {
		return []byte(prodDummyHash) // unreachable: fixed plaintext, valid cost
	}
	return h
}

// HashPassword hashes pw with bcrypt at bcryptCost. bcrypt rejects passwords
// longer than 72 bytes with bcrypt.ErrPasswordTooLong; callers must map that
// to a 400, not a 500.
func HashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcryptCost)
	return string(h), err
}

// CheckPassword is constant-shape: empty hashes (SSO-only users) still burn a
// bcrypt comparison and always fail.
func CheckPassword(hash, pw string) bool {
	if hash == "" {
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(pw))
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// CheckDummy burns one bcrypt comparison (unknown-user path).
func CheckDummy(pw string) { _ = bcrypt.CompareHashAndPassword(dummyHash, []byte(pw)) }
