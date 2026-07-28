package health

// tierRank orders tiers by criticality: lower rank is MORE critical. Unknown
// tiers rank last so they never win a conflict.
func tierRank(t Tier) int {
	switch t {
	case TierT0:
		return 0
	case TierT1:
		return 1
	case TierT2:
		return 2
	case TierT3:
		return 3
	default:
		return 4
	}
}

// moreCritical returns the more critical of two tiers. It is the group conflict
// rule: members of one group may declare different tiers, and a group holding a
// T0 service is a T0 group. Understating criticality is the dangerous
// direction, so the most critical member wins.
func moreCritical(a, b Tier) Tier {
	if tierRank(b) < tierRank(a) {
		return b
	}
	return a
}

// parseTierSoft validates a DECLARED tier. Unlike Config.Validate, which fails
// the hub loud on operator typos, this never errors: declarations arrive from
// application telemetry with no review gate, and one team shipping
// `avuru.tier: T9` must not take the health board down for everyone. The caller
// falls back to the default tier and surfaces a warning.
func parseTierSoft(v string) (Tier, bool) {
	t := Tier(v)
	if knownTiers[t] {
		return t, true
	}
	return "", false
}
