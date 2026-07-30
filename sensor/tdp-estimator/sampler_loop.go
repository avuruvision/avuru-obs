package main

import (
	"log/slog"
	"time"
)

// runSampler is the estimator's main loop: on each tick, read node
// utilization and per-pod cgroup usage, compute power via the model, and
// integrate into the registry's cumulative joules counters. Runs for the
// process lifetime (main.go only calls this once RAPL has been ruled out).
func runSampler(nodeName string, interval time.Duration, coeff Coefficients, reg *registry) {
	client := newKubeletClient()
	token, err := readServiceAccountToken()
	if err != nil {
		slog.Error("cannot read service account token, pod attribution disabled (node energy still reported)", "error", err)
	}
	baseURL := kubeletBaseURL(nodeName)

	var (
		prevCPU       cpuTimes
		havePrevCPU   bool
		havePrevW     bool
		prevNodeW     float64 // last tick's node wattage, for trapezoidal integration
		nodeJoules    float64
		podPrevUsage  = map[string]uint64{}      // uid -> previous usage_usec
		podJoules     = map[string]float64{}     // uid -> cumulative joules
		podIdentities = map[string]podIdentity{} // uid -> last known name/namespace, so an exited pod can be removed from the registry by identity
	)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		cur, err := readCPUTimes(defaultProcStat)
		if err != nil {
			slog.Warn("reading /proc/stat failed, skipping this tick", "error", err)
			continue
		}
		if !havePrevCPU {
			prevCPU, havePrevCPU = cur, true
			continue // need two samples before a utilization delta exists
		}
		util := utilizationDelta(prevCPU, cur)
		nodeW := nodePower(coeff, util)
		// Integrate ONLY this tick's increment (prevNodeW -> nodeW over
		// exactly `interval`), then accumulate — NOT the wattage times total
		// elapsed process lifetime, which would re-integrate the whole
		// history on every tick. The ticker's own fixed interval IS the dt
		// between consecutive samples, so no wall-clock timestamps are
		// needed here at all.
		if !havePrevW {
			prevNodeW, havePrevW = nodeW, true
		}
		nodeJoules += integrateJoules([]wattSample{{atSeconds: 0, watts: prevNodeW}, {atSeconds: interval.Seconds(), watts: nodeW}})
		prevNodeW = nodeW
		reg.setNodeEnergy(nodeName, nodeJoules)

		// Pod attribution: best-effort. A kubelet or cgroup-walk failure
		// degrades to node-only reporting (still useful) rather than
		// crashing the estimator — matches the AEP's "do no harm" posture.
		pods, err := fetchPods(client, baseURL, token)
		if err != nil {
			slog.Warn("kubelet pod list unavailable, pod-level energy skipped this tick", "error", err)
			prevCPU = cur
			continue
		}
		cgroups, err := discoverPodCgroups(defaultCgroupRoot)
		if err != nil {
			slog.Warn("cgroup discovery failed, pod-level energy skipped this tick", "error", err)
			prevCPU = cur
			continue
		}

		nodeActiveSecs := util * interval.Seconds()
		seen := map[string]bool{}
		for _, cg := range cgroups {
			id, known := pods[cg.uid]
			if !known {
				continue // cgroup exists but the kubelet hasn't reported this pod yet (race on create/delete)
			}
			usage, err := readCPUStatUsage(cg.path)
			if err != nil {
				continue
			}
			seen[cg.uid] = true
			deltaUsec := float64(0)
			if prev, ok := podPrevUsage[cg.uid]; ok && usage >= prev {
				deltaUsec = float64(usage - prev)
			}
			podPrevUsage[cg.uid] = usage
			podIdentities[cg.uid] = id
			podActiveSecs := deltaUsec / 1_000_000
			podW := podDynamicPower(nodeW, coeff.IdleWatts, podShareOfActive(podActiveSecs, nodeActiveSecs))
			podJoules[cg.uid] += podW * interval.Seconds()
			reg.setPodEnergy(id.Name, id.Namespace, podJoules[cg.uid])
		}
		// Pods whose cgroup disappeared (exited) stop being reported — both
		// from this loop's own bookkeeping AND from the registry (otherwise
		// /metrics would keep exporting an exited pod's last value forever),
		// matching Kepler's own behavior on pod exit.
		for uid := range podPrevUsage {
			if !seen[uid] {
				if id, ok := podIdentities[uid]; ok {
					reg.removePod(id.Name, id.Namespace)
				}
				delete(podPrevUsage, uid)
				delete(podJoules, uid)
				delete(podIdentities, uid)
			}
		}

		prevCPU = cur
	}
}
