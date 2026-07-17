package migrations

import (
	"testing"

	"github.com/avuru/avuru-obs/hub/internal/modules"
)

// TestByModuleCoversOrdered guards the migration↔module mapping: an untagged
// migration would silently apply for every install (or worse, a stale tag
// would point at a module that no longer exists).
func TestByModuleCoversOrdered(t *testing.T) {
	known := map[modules.Name]bool{}
	for _, m := range modules.All {
		known[m] = true
	}

	for _, version := range Ordered {
		mods, ok := ByModule[version]
		if !ok || len(mods) == 0 {
			t.Errorf("migration %s has no module tag in ByModule", version)
			continue
		}
		for _, mod := range mods {
			if !known[mod] {
				t.Errorf("migration %s tagged with unknown module %q", version, mod)
			}
		}
	}
	for version := range ByModule {
		found := false
		for _, v := range Ordered {
			if v == version {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("ByModule entry %s is not in Ordered", version)
		}
	}
}
