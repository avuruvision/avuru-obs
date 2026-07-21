package auth

import "golang.org/x/crypto/bcrypt"

// bcryptCost 12 ≈ 250ms/hash on current hardware — login-path acceptable,
// brute-force hostile.
const bcryptCost = 12

// dummyHash is a fixed cost-12 bcrypt hash compared when the user does not
// exist or has no password, so login latency does not reveal account
// existence. The plaintext is irrelevant — only the comparison cost matters.
var dummyHash = []byte("$2a$12$crQm6wFj4lek4Cvpwn7/fuzoq6G9Edre2z3XEcAT9h4Ul3pxrFqQe")

// HashPassword hashes pw with bcrypt cost 12. bcrypt rejects passwords longer
// than 72 bytes with bcrypt.ErrPasswordTooLong; callers must map that to a
// 400, not a 500.
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
