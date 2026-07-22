package green

// Bundled grid carbon-intensity defaults (gCO2e/kWh).
//
// Provenance: rounded per-country annual averages from Ember's yearly
// electricity data (ember-energy.org, 2023/2024 vintage), cross-checked
// against EEA figures for EU members. They exist so an air-gapped install
// reports a defensible number with zero egress and zero config — but they are
// deliberately coarse (rounded, annual, national): operators should set
// gridIntensity to the factor their provider or national registry publishes,
// which always wins over this table (see Config.EffectiveIntensity). The
// dataset label below is quoted verbatim in the CSRD export's methodology
// block as factor provenance.

// intensityDataset names the source + vintage in EffectiveIntensity labels;
// it must stay consistent with the 2023/2024 vintage stated above.
const intensityDataset = "Ember 2023/24"

// regionWorld is the pseudo-region for the global average — the fallback when
// no region is configured (also accepted explicitly as region: "WORLD").
const regionWorld = "WORLD"

// worldIntensity is the global average grid intensity (gCO2e/kWh).
const worldIntensity = 480

// countryIntensity maps uppercase ISO 3166-1 alpha-2 codes to annual-average
// grid intensity (gCO2e/kWh).
var countryIntensity = map[string]float64{
	// EU members.
	"AT": 110, // Austria — hydro-heavy
	"BE": 130, // Belgium — nuclear-heavy
	"BG": 400, // Bulgaria
	"CZ": 410, // Czechia — coal + nuclear
	"DE": 380, // Germany
	"DK": 150, // Denmark — wind-heavy
	"EE": 460, // Estonia — oil shale, falling fast
	"ES": 160, // Spain
	"FI": 70,  // Finland — nuclear + hydro
	"FR": 50,  // France — nuclear
	"GR": 340, // Greece
	"HR": 200, // Croatia
	"HU": 200, // Hungary — nuclear-heavy
	"IE": 280, // Ireland
	"IT": 330, // Italy
	"LT": 160, // Lithuania
	"LU": 120, // Luxembourg — import-dominated
	"LV": 120, // Latvia — hydro-heavy
	"NL": 270, // Netherlands
	"PL": 660, // Poland — coal
	"PT": 130, // Portugal
	"RO": 250, // Romania
	"SE": 30,  // Sweden — hydro + nuclear
	"SI": 230, // Slovenia
	"SK": 110, // Slovakia — nuclear-heavy

	// Rest of Europe.
	"CH": 35,  // Switzerland — hydro + nuclear
	"GB": 220, // United Kingdom
	"NO": 30,  // Norway — hydro
	"TR": 430, // Türkiye

	// Americas.
	"BR": 100, // Brazil — hydro
	"CA": 130, // Canada — hydro-heavy
	"MX": 430, // Mexico
	"US": 370, // United States

	// Asia-Pacific, Middle East, Africa.
	"AU": 550, // Australia
	"CN": 560, // China
	"ID": 680, // Indonesia — coal
	"IN": 710, // India — coal
	"JP": 460, // Japan
	"KR": 430, // South Korea
	"NZ": 110, // New Zealand — hydro + geothermal
	"SG": 470, // Singapore — gas
	"TW": 560, // Taiwan
	"ZA": 700, // South Africa — coal
}
