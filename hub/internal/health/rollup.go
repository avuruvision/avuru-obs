package health

import (
	"fmt"
	"sort"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// Dependency is one critical dependency of a service, with the dependency's
// own (base) status — enough for the UI to render the "why" chain directly.
type Dependency struct {
	Service  string
	Tier     Tier
	Critical bool
	Status   string
}

// Member is one service's health inside a group. BaseStatus is the service on
// its own; EffectiveStatus folds in critical-dependency propagation.
type Member struct {
	Service         string
	Tier            Tier
	BaseStatus      string
	EffectiveStatus string
	Reason          string // reason for the EFFECTIVE status
	SpanCount       uint64
	RatePerSec      float64
	ErrorRate       float64
	P95Ms           float64
	Dependencies    []Dependency
}

// GroupHealth is one group's rolled-up health. Identity is the pair
// (Name, Environment): Name stays the domain so name-keyed consumers (the
// group-detail route, alerting selectors, green budgets) keep matching, and
// Environment carries the new dimension.
type GroupHealth struct {
	Name        string
	Environment string
	Tier        Tier
	Source      string // "config" | "auto"
	TierSource  string // "override" | "config" | "declared" | "default"
	Status      string
	Reason      string
	Counts      map[string]int // per-status member tally
	SpanCount   uint64
	RatePerSec  float64
	ErrorRate   float64
	P95Ms       float64
	Members     []Member
}

// Report is the whole tenant's group health for a window.
type Report struct {
	Overall string
	Groups  []GroupHealth
}

// Rollup computes group health from the RED population (stats), grouping
// labels, and dependency edges, over a window. It is pure: no I/O, no clock.
// The window is used only to derive per-second rates.
func Rollup(cfg Config, window time.Duration, stats []storage.ServiceStats, labels []storage.ServiceLabel, edges []storage.ServiceEdge) Report {
	assign, _ := resolve(cfg, stats, labels)
	statByService := make(map[string]storage.ServiceStats, len(stats))
	for _, s := range stats {
		statByService[s.Name] = s
	}

	// Base status + reason per service.
	base := make(map[string]string, len(stats))
	baseReason := make(map[string]string, len(stats))
	for _, s := range stats {
		a := assign[s.Name]
		thr := cfg.ResolveThresholds(s.Name, a.Tier)
		st, reason := serviceStatus(s, thr)
		base[s.Name], baseReason[s.Name] = st, reason
	}

	deps, effective, effReason := propagate(cfg, assign, base, baseReason, edges)

	members := buildMembers(assign, statByService, base, effective, effReason, deps, window)
	groups := groupAndRoll(assign, members)

	return Report{Overall: overallStatus(groups), Groups: groups}
}

// propagate applies the golden rule: an edge s→t is critical iff t is T0 (or a
// configured override); if a critical dependency's BASE status is down, s is
// worsened to at least degraded. Reading base (never effective) makes this a
// single terminating pass even with cycles. Returns per-service critical
// dependencies, effective statuses, and effective reasons.
func propagate(cfg Config, assign map[string]assignment, base, baseReason map[string]string, edges []storage.ServiceEdge) (map[string][]Dependency, map[string]string, map[string]string) {
	overrides := cfg.criticalOverrides()
	effective := make(map[string]string, len(base))
	effReason := make(map[string]string, len(base))
	for svc, st := range base {
		effective[svc], effReason[svc] = st, baseReason[svc]
	}

	deps := map[string][]Dependency{}
	seen := map[[2]string]bool{} // dedup (source,target): edges arrive from trace+flow

	for _, e := range edges {
		if e.Source == "" || e.Target == "" || e.Source == e.Target {
			continue
		}
		key := [2]string{e.Source, e.Target}
		if seen[key] {
			continue
		}
		seen[key] = true

		targetTier := assign[e.Target].Tier
		critical := targetTier == TierT0
		if overrides != nil && overrides[key] {
			critical = true
		}
		if !critical {
			continue
		}

		depStatus, known := base[e.Target]
		if !known {
			depStatus = StatusUnknown
		}
		deps[e.Source] = append(deps[e.Source], Dependency{
			Service: e.Target, Tier: targetTier, Critical: true, Status: depStatus,
		})

		if depStatus != StatusDown {
			continue
		}
		// A critical dependency is down: the dependent cannot be green.
		switch effective[e.Source] {
		case StatusIdle, StatusUnknown:
			// No signal of our own — annotate, don't fabricate a status.
			effReason[e.Source] = fmt.Sprintf("critical dependency %s is down", e.Target)
		case StatusHealthy:
			effective[e.Source] = StatusDegraded
			effReason[e.Source] = fmt.Sprintf("depends on %s (down)", e.Target)
		default: // already degraded/down — keep, but note the dependency too
			effReason[e.Source] = fmt.Sprintf("%s; depends on %s (down)", effReason[e.Source], e.Target)
		}
	}
	return deps, effective, effReason
}

// buildMembers assembles the per-service Member view with derived RED numbers.
func buildMembers(assign map[string]assignment, statByService map[string]storage.ServiceStats, base, effective, effReason map[string]string, deps map[string][]Dependency, window time.Duration) map[string]Member {
	secs := window.Seconds()
	if secs <= 0 {
		secs = 1
	}
	out := make(map[string]Member, len(assign))
	for svc, a := range assign {
		s := statByService[svc]
		var errRate float64
		if s.SpanCount > 0 {
			errRate = float64(s.ErrorCount) / float64(s.SpanCount)
		}
		d := deps[svc]
		sort.Slice(d, func(i, j int) bool { return d[i].Service < d[j].Service })
		out[svc] = Member{
			Service:         svc,
			Tier:            a.Tier,
			BaseStatus:      base[svc],
			EffectiveStatus: effective[svc],
			Reason:          effReason[svc],
			SpanCount:       s.SpanCount,
			RatePerSec:      float64(s.SpanCount) / secs,
			ErrorRate:       errRate,
			P95Ms:           float64(s.P95) / float64(time.Millisecond),
			Dependencies:    d,
		}
	}
	return out
}

// groupKey is a group's composite identity. An empty environment collapses to
// the bare group name so installs that declare nothing keep today's keys.
func groupKey(group, env string) string {
	if env == "" {
		return group
	}
	return group + "\x00" + env
}

// groupAndRoll buckets members by (group, environment) and computes each
// group's rolled-up status (worst-of effective, ignoring idle) and aggregate
// RED. The group's tier is its MOST CRITICAL member's tier.
func groupAndRoll(assign map[string]assignment, members map[string]Member) []GroupHealth {
	type acc struct {
		name, env         string
		tier              Tier
		source            string
		tierSource        string
		members           []Member
		spanCount, errCnt uint64
		rate, p95         float64
	}
	byGroup := map[string]*acc{}
	order := []string{}
	for svc, a := range assign {
		k := groupKey(a.Group, a.Environment)
		g, ok := byGroup[k]
		if !ok {
			g = &acc{name: a.Group, env: a.Environment, tier: a.Tier, source: a.Source, tierSource: a.TierSource}
			byGroup[k] = g
			order = append(order, k)
		} else if t := moreCritical(g.tier, a.Tier); t != g.tier {
			// A more critical member takes over the group's tier and its provenance.
			g.tier, g.tierSource = t, a.TierSource
		}
		m := members[svc]
		g.members = append(g.members, m)
		g.spanCount += m.SpanCount
		g.errCnt += uint64(m.ErrorRate * float64(m.SpanCount))
		g.rate += m.RatePerSec
		if m.P95Ms > g.p95 {
			g.p95 = m.P95Ms // conservative group headline: worst member p95
		}
	}
	sort.Strings(order)

	out := make([]GroupHealth, 0, len(order))
	for _, k := range order {
		g := byGroup[k]
		sort.Slice(g.members, func(i, j int) bool { return g.members[i].Service < g.members[j].Service })
		status, counts := rollUpMembers(g.members)
		var errRate float64
		if g.spanCount > 0 {
			errRate = float64(g.errCnt) / float64(g.spanCount)
		}
		out = append(out, GroupHealth{
			Name:        g.name,
			Environment: g.env,
			Tier:        g.tier,
			Source:      g.source,
			TierSource:  g.tierSource,
			Status:      status,
			Reason:      groupReason(status, g.members),
			Counts:      counts,
			SpanCount:   g.spanCount,
			RatePerSec:  g.rate,
			ErrorRate:   errRate,
			P95Ms:       g.p95,
			Members:     g.members,
		})
	}
	return out
}

// rollUpMembers returns the group status (worst-of effective, ignoring idle;
// all-idle → idle) and a per-status tally.
func rollUpMembers(members []Member) (string, map[string]int) {
	counts := map[string]int{}
	status := StatusIdle
	anyReal := false
	for _, m := range members {
		counts[m.EffectiveStatus]++
		if severity(m.EffectiveStatus) > 0 {
			anyReal = true
			status = worst(status, m.EffectiveStatus)
		}
	}
	if !anyReal {
		return StatusIdle, counts
	}
	return status, counts
}

// groupReason names the worst member driving the group status.
func groupReason(status string, members []Member) string {
	if status == StatusIdle {
		return "no traffic in window"
	}
	var worstM *Member
	for i := range members {
		m := &members[i]
		if worstM == nil || severity(m.EffectiveStatus) > severity(worstM.EffectiveStatus) {
			worstM = m
		}
	}
	if worstM == nil {
		return ""
	}
	if status == StatusHealthy {
		return fmt.Sprintf("all %d services within objectives", len(members))
	}
	return fmt.Sprintf("%s is %s", worstM.Service, worstM.EffectiveStatus)
}

// overallStatus is worst-of group statuses (idle groups ignored; all idle →
// idle).
func overallStatus(groups []GroupHealth) string {
	status := StatusIdle
	anyReal := false
	for _, g := range groups {
		if severity(g.Status) > 0 {
			anyReal = true
			status = worst(status, g.Status)
		}
	}
	if !anyReal {
		return StatusIdle
	}
	return status
}
