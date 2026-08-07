package auth

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// The production cost is a security parameter. Pin it so the test-mode
// override below can never be mistaken for licence to lower the real one.
func TestProductionBcryptCostIsTwelve(t *testing.T) {
	if productionBcryptCost != 12 {
		t.Fatalf("productionBcryptCost = %d, want 12", productionBcryptCost)
	}
}

// Every suite that bootstraps an admin, logs in, or creates a user pays this
// cost per hash AND per compare. A cost-12 pair costs ~5.5s under -race, which
// is what pushed internal/api past go test's 10-minute per-package timeout.
func TestTestBinariesUseCheapBcryptCost(t *testing.T) {
	if bcryptCost != bcrypt.MinCost {
		t.Fatalf("bcryptCost = %d in a test binary, want bcrypt.MinCost (%d)", bcryptCost, bcrypt.MinCost)
	}
}

// bcrypt reads the cost from the hash, not from the caller, so a dummy pinned
// at cost 12 would keep the unknown-user and SSO-user login paths expensive
// however cheap bcryptCost gets.
func TestDummyHashTracksBcryptCost(t *testing.T) {
	got, err := bcrypt.Cost(dummyHash)
	if err != nil {
		t.Fatalf("bcrypt.Cost(dummyHash): %v", err)
	}
	if got != bcryptCost {
		t.Fatalf("dummyHash cost = %d, want %d", got, bcryptCost)
	}
	// Production uses the literal verbatim and never re-derives it, so it has
	// to stay a valid hash at the production cost.
	c, err := bcrypt.Cost([]byte(prodDummyHash))
	if err != nil || c != productionBcryptCost {
		t.Fatalf("prodDummyHash cost = %d (err %v), want %d", c, err, productionBcryptCost)
	}
}

// The empty-hash branch is an unconditional refusal, not a comparison whose
// result is returned: the dummy's plaintext is now a known constant, so
// leaking that comparison would let anyone log in as any SSO-only user.
func TestCheckPasswordRefusesEmptyHashEvenForDummyPlaintext(t *testing.T) {
	if CheckPassword("", "avuru-obs/no-such-user") {
		t.Fatal("CheckPassword accepted the dummy plaintext against an empty hash")
	}
}

// The cost override must not break the hash/verify round trip.
func TestHashPasswordRoundTrips(t *testing.T) {
	h, err := HashPassword("root-pw")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !CheckPassword(h, "root-pw") {
		t.Fatal("CheckPassword rejected the password it just hashed")
	}
	if CheckPassword(h, "wrong-pw") {
		t.Fatal("CheckPassword accepted a wrong password")
	}
}
