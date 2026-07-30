package main

import "testing"

func TestResolve_AnnotationWins(t *testing.T) {
	c := Resolve(
		map[string]string{"obs.avuru.io/power-idle-watts": "10", "obs.avuru.io/power-max-watts": "50"},
		5, 40, // Helm values present too, but annotation must win
		"Intel(R) Xeon(R) Platinum 8259CL CPU @ 2.50GHz",
	)
	if c.Tier != "annotation" || c.IdleWatts != 10 || c.MaxWatts != 50 {
		t.Errorf("Resolve = %+v, want tier=annotation idle=10 max=50", c)
	}
}

func TestResolve_ValuesWinOverTable(t *testing.T) {
	c := Resolve(nil, 7, 42, "Intel(R) Xeon(R) Platinum 8259CL CPU @ 2.50GHz")
	if c.Tier != "values" || c.IdleWatts != 7 || c.MaxWatts != 42 {
		t.Errorf("Resolve = %+v, want tier=values idle=7 max=42", c)
	}
}

func TestResolve_TableMatch(t *testing.T) {
	// 8259CL is a real Cascade Lake AWS c5 SKU; the table matches on the
	// "Cascade Lake" family, not the exact SKU number.
	c := Resolve(nil, 0, 0, "Intel(R) Xeon(R) Platinum 8259CL CPU @ 2.50GHz")
	if c.Tier != "table" {
		t.Fatalf("Tier = %q, want table", c.Tier)
	}
	if c.IdleWatts != 0.64 || c.MaxWatts != 3.97 {
		t.Errorf("Resolve = %+v, want the bundled Cascade Lake entry (0.64/3.97)", c)
	}
}

func TestResolve_GenericFallback(t *testing.T) {
	c := Resolve(nil, 0, 0, "Some Exotic CPU Nobody Has Modeled Yet")
	if c.Tier != "fallback" {
		t.Fatalf("Tier = %q, want fallback", c.Tier)
	}
	if c.IdleWatts <= 0 || c.MaxWatts <= c.IdleWatts {
		t.Errorf("fallback coefficients look wrong: %+v", c)
	}
}

func TestMatchArchitecture(t *testing.T) {
	tests := []struct {
		model string
		want  string // "" means no match (falls through to fallback)
	}{
		{"Intel(R) Xeon(R) Platinum 8259CL CPU @ 2.50GHz", "CASCADE_LAKE"},
		{"Intel(R) Xeon(R) CPU E5-2670 v2 @ 2.50GHz", "IVY_BRIDGE"},
		{"AMD EPYC 7551 32-Core Processor", "AMD_EPYC_1ST_GEN"}, // verified Naples/1st-gen SKU
		{"AMD EPYC 7742 64-Core Processor", "AMD_EPYC_2ND_GEN"}, // verified Rome/2nd-gen SKU
		{"AMD EPYC 7763 64-Core Processor", "AMD_EPYC_3RD_GEN"}, // verified Milan/3rd-gen SKU
		{"Some Exotic CPU Nobody Has Modeled Yet", ""},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			if got := matchArchitecture(tt.model); got != tt.want {
				t.Errorf("matchArchitecture(%q) = %q, want %q", tt.model, got, tt.want)
			}
		})
	}
}
