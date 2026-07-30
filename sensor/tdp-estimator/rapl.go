package main

import (
	"os"
	"path/filepath"
)

// defaultRAPLGlob is where the powercap sysfs exposes Intel RAPL energy
// counters on hardware that has them.
const defaultRAPLGlob = "/sys/class/powercap/intel-rapl*"

// raplPresent reports whether the node exposes at least one readable RAPL
// energy counter. When true, Kepler measures real power and this estimator
// must stay dormant for the whole process lifetime — a node's power
// interface doesn't change under a running pod, so callers probe once at
// startup, not on a loop (see main.go).
func raplPresent(glob string) bool {
	matches, err := filepath.Glob(glob)
	if err != nil || len(matches) == 0 {
		return false
	}
	for _, zone := range matches {
		f, err := os.Open(filepath.Join(zone, "energy_uj"))
		if err != nil {
			continue
		}
		f.Close()
		return true
	}
	return false
}
