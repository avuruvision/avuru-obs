package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRAPLPresent(t *testing.T) {
	t.Run("no matching path", func(t *testing.T) {
		if raplPresent(filepath.Join(t.TempDir(), "intel-rapl*")) {
			t.Error("raplPresent() = true, want false when glob matches nothing")
		}
	})

	t.Run("dir exists but no energy_uj file", func(t *testing.T) {
		dir := t.TempDir()
		zone := filepath.Join(dir, "intel-rapl:0")
		if err := os.Mkdir(zone, 0o755); err != nil {
			t.Fatal(err)
		}
		if raplPresent(filepath.Join(dir, "intel-rapl*")) {
			t.Error("raplPresent() = true, want false when energy_uj is missing")
		}
	})

	t.Run("energy_uj readable", func(t *testing.T) {
		dir := t.TempDir()
		zone := filepath.Join(dir, "intel-rapl:0")
		if err := os.Mkdir(zone, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(zone, "energy_uj"), []byte("123456\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if !raplPresent(filepath.Join(dir, "intel-rapl*")) {
			t.Error("raplPresent() = false, want true when energy_uj is readable")
		}
	})
}
