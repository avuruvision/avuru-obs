package green

import (
	"strings"
	"testing"
)

// TestIntensityTableCoverage pins the regions the AEP promises out of the box.
func TestIntensityTableCoverage(t *testing.T) {
	required := []string{
		// EU members of meaningful size.
		"SE", "FR", "DE", "PL", "ES", "IT", "NL", "BE", "AT", "DK",
		"FI", "IE", "PT", "CZ", "RO", "HU", "GR", "LU",
		// Beyond the EU.
		"US", "CA", "GB", "NO", "CH", "CN", "IN", "JP", "KR", "AU",
		"BR", "ZA", "SG",
	}
	for _, r := range required {
		if _, ok := countryIntensity[r]; !ok {
			t.Errorf("bundled intensity table missing %s", r)
		}
	}
	if len(countryIntensity) < 40 {
		t.Errorf("bundled intensity table has %d entries, want >= 40", len(countryIntensity))
	}
}

// TestIntensityTableSanity guards against transposed or implausible values:
// hydro/nuclear grids read low, coal grids read high, world sits mid-range.
func TestIntensityTableSanity(t *testing.T) {
	for code, v := range countryIntensity {
		if len(code) != 2 || code != strings.ToUpper(code) {
			t.Errorf("region key %q is not an uppercase ISO 3166-1 alpha-2 code", code)
		}
		if v <= 0 || v >= 1000 {
			t.Errorf("intensity for %s = %v gCO2e/kWh is implausible", code, v)
		}
	}
	for _, low := range []string{"SE", "NO", "FR", "CH"} {
		if v := countryIntensity[low]; v >= 100 {
			t.Errorf("%s should be a low-carbon grid (< 100), got %v", low, v)
		}
	}
	for _, high := range []string{"PL", "IN", "ZA"} {
		if v := countryIntensity[high]; v < 600 {
			t.Errorf("%s should be a coal-heavy grid (>= 600), got %v", high, v)
		}
	}
	if worldIntensity < 400 || worldIntensity > 550 {
		t.Errorf("world average %v out of the plausible 400-550 band", worldIntensity)
	}
}
