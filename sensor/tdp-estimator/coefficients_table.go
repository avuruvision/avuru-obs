package main

// Bundled per-CPU-architecture power coefficients (idle/max watts per
// thread, from SPECpower_ssj2008-derived data), used to estimate node power
// via P = P_idle + u*(P_max-P_idle) when no operator override applies. Same
// bundled-dataset pattern as hub/internal/green/intensity.go's grid-carbon
// table: hand-authored, cited per entry, versioned by the constant below.
//
// Provenance: Cloud Carbon Footprint's Apache-2.0 coefficient data
// (github.com/cloud-carbon-footprint/cloud-carbon-footprint,
// AwsFootprintEstimationConstants.ts / GcpFootprintEstimationConstants.ts /
// AzureFootprintEstimationConstants.ts, fetched 2026-07-30), cross-checked
// against the original SPECpower-derived notebook
// (github.com/cloud-carbon-footprint/cloud-carbon-coefficients,
// coefficients.ipynb) whose own embedded `assert` statements independently
// confirm several entries. Where AWS/GCP's live files disagree with
// Azure's file AND the notebook's asserted output (Ivy Bridge, Haswell — up
// to ~78% apart, no public changelog found explaining the divergence), the
// Azure/notebook values are used as the more methodology-traceable source.
// AMD_EPYC_5TH_GEN is deliberately NOT included: AWS's file (3.68W/8.96W)
// and GCP's file (0.27W/1.36W) disagree by nearly an order of magnitude with
// no way to adjudicate — it falls through to the generic fallback tier
// rather than shipping either unverifiable number (the AEP's honesty
// principle: omit rather than guess).
const coefficientDataset = "Cloud Carbon Footprint 2026-07 (Azure/notebook preferred on conflict)"

// archCoefficients is keyed by an internal architecture identifier (not a
// vendor SKU) — matchArchitecture maps a raw /proc/cpuinfo "model name"
// string to one of these keys.
var archCoefficients = map[string]Coefficients{
	// Matches Azure's file AND the SPECpower notebook's own asserted output
	// exactly (cross-validated independently, see coefficients_test.go).
	"CASCADE_LAKE":     {IdleWatts: 0.64, MaxWatts: 3.97}, // Azure L47/L84; notebook cell 32
	"SKYLAKE":          {IdleWatts: 0.65, MaxWatts: 4.26}, // Azure L48/L85
	"BROADWELL":        {IdleWatts: 0.71, MaxWatts: 3.69}, // Azure L49/L86; notebook cell 28
	"COFFEE_LAKE":      {IdleWatts: 1.14, MaxWatts: 5.42}, // Azure L51/L88; notebook cell 34, cross-confirmed by ccf-coefficients README worked example
	"SANDY_BRIDGE":     {IdleWatts: 2.17, MaxWatts: 8.58}, // Azure L52/L89; notebook cell 22
	"AMD_EPYC_1ST_GEN": {IdleWatts: 0.82, MaxWatts: 2.55}, // Azure L54/L91; notebook cell 16
	"AMD_EPYC_2ND_GEN": {IdleWatts: 0.47, MaxWatts: 1.69}, // Azure L55/L92; notebook cell 18

	// Azure/notebook values preferred over AWS/GCP's diverging live figures
	// (AWS/GCP: Ivy Bridge 1.71/5.51-5.56, Haswell 1.86-1.90/5.56-5.60).
	"IVY_BRIDGE": {IdleWatts: 3.04, MaxWatts: 8.25}, // Azure L53/L90; notebook cell 24 exact match
	"HASWELL":    {IdleWatts: 1.00, MaxWatts: 4.74}, // Azure L50/L87 (only source; notebook cell 26 says 1.90/6.01 — Azure preferred per sourcing decision)

	// AMD_EPYC_3RD_GEN: Azure/notebook (0.45/2.02) vs AWS (0.46/1.96) vs GCP
	// (0.46/1.83) — all close; Azure/notebook value used for consistency
	// with the rest of this table's sourcing policy.
	"AMD_EPYC_3RD_GEN": {IdleWatts: 0.45, MaxWatts: 2.02}, // Azure L56/L93; notebook cell 20

	// AWS-only entries (no Azure coverage to conflict with; GCP's figures
	// for these agree closely with AWS, so AWS's are used directly).
	"AMD_EPYC_4TH_GEN": {IdleWatts: 0.74, MaxWatts: 2.28}, // AWS L64/L121 (GCP: 0.74/2.2, close)
	"EMERALD_RAPIDS":   {IdleWatts: 0.81, MaxWatts: 4.48}, // AWS L66/L123 (GCP: 0.81/4.38, close)
	"GRANITE_RAPIDS":   {IdleWatts: 0.58, MaxWatts: 2.53}, // AWS L67/L124 (GCP: 0.58/2.37, close)
	"ICELAKE":          {IdleWatts: 0.77, MaxWatts: 3.76}, // AWS L73/L130 (GCP: 0.77/3.65, close)
	"SAPPHIRE_RAPIDS":  {IdleWatts: 1.04, MaxWatts: 4.16}, // AWS L76/L133 (GCP: 1.04/4.06, close)
	"AWS_GRAVITON_2":   {IdleWatts: 0.47, MaxWatts: 1.69}, // AWS L68/L125 (AWS-proprietary silicon, no GCP/Azure equivalent)
	"AWS_GRAVITON_3":   {IdleWatts: 0.47, MaxWatts: 1.69}, // AWS L69/L126
	"AWS_GRAVITON_3E":  {IdleWatts: 0.47, MaxWatts: 1.69}, // AWS L70/L127
	"AWS_GRAVITON_4":   {IdleWatts: 0.47, MaxWatts: 1.69}, // AWS L71/L128
	"APPLE":            {IdleWatts: 6.8, MaxWatts: 39},    // AWS L57/L114 (EC2 Mac instances)
}

// genericFallback is the loud, widest-error-band tier-4 default (AEP: "allowed
// but loud" — main.go / coefficients.go log a warning whenever this is used).
// Taken from AWS's file's own fallback-default figures (MIN/MAX_WATTS_AVG,
// L54/L111); GCP's (0.68/4.11 median) and Azure's (0.74/3.54 average)
// fallbacks are close enough that picking one rather than averaging avoids
// synthesizing an unsourced blended number.
var genericFallback = Coefficients{IdleWatts: 0.74, MaxWatts: 3.5}
