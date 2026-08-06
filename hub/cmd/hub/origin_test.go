package main

import (
	"slices"
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/api"
)

// The env plumbing is where a rename or a typo would go unnoticed: the hub
// would boot green and 403 every write.

func TestOriginCheckModeDefaultsToEnforce(t *testing.T) {
	t.Setenv("AVURUOBS_AUTH_ORIGIN_CHECK", "")
	mode, err := originCheckMode()
	if err != nil {
		t.Fatal(err)
	}
	if mode != api.OriginCheckEnforce {
		t.Fatalf("mode %q, want %q", mode, api.OriginCheckEnforce)
	}
}

func TestOriginCheckModeAcceptsLoweredModes(t *testing.T) {
	for _, want := range []string{api.OriginCheckLog, api.OriginCheckOff} {
		t.Setenv("AVURUOBS_AUTH_ORIGIN_CHECK", want)
		mode, err := originCheckMode()
		if err != nil {
			t.Fatalf("%s: %v", want, err)
		}
		if mode != want {
			t.Fatalf("mode %q, want %q", mode, want)
		}
	}
}

// A typo must fail the boot: silently falling back to enforce would leave an
// operator staring at 403s they believe they turned off.
func TestOriginCheckModeRejectsTypo(t *testing.T) {
	t.Setenv("AVURUOBS_AUTH_ORIGIN_CHECK", "Off")
	if _, err := originCheckMode(); err == nil {
		t.Fatal("typo accepted, want an error")
	}
}

func TestTrustedOriginsIncludesPublicURL(t *testing.T) {
	t.Setenv("AVURUOBS_AUTH_TRUSTED_ORIGINS", "https://a.example, https://b.example")
	t.Setenv("AVURUOBS_PUBLIC_URL", "https://sso.example")
	got := trustedOrigins()
	for _, want := range []string{"https://a.example", "https://b.example", "https://sso.example"} {
		if !slices.Contains(got, want) {
			t.Fatalf("trusted origins %v, missing %q", got, want)
		}
	}
}

func TestTrustedOriginsUnsetIsEmpty(t *testing.T) {
	t.Setenv("AVURUOBS_AUTH_TRUSTED_ORIGINS", "")
	t.Setenv("AVURUOBS_PUBLIC_URL", "")
	if got := trustedOrigins(); len(got) != 0 {
		t.Fatalf("trusted origins %v, want empty", got)
	}
}
