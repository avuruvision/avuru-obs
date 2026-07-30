package main

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// defaultCgroupRoot is where the host's cgroup v2 hierarchy is bind-mounted
// read-only into the container (values.yaml: sensor.green.estimation, same
// host-mount convention as Kepler's powercap/sysfs mounts).
const defaultCgroupRoot = "/sys/fs/cgroup"

// podCgroupSystemd matches the systemd cgroup driver's pod-slice naming,
// e.g. "kubepods-burstable-pod12345678_1234_1234_1234_123456789012.slice"
// (guaranteed-QoS pods omit the burstable/besteffort segment:
// "kubepods-pod<uid>.slice"). Captures the underscored UID.
var podCgroupSystemd = regexp.MustCompile(`kubepods(?:-(?:burstable|besteffort))?-pod([0-9a-fA-F]{8}_[0-9a-fA-F]{4}_[0-9a-fA-F]{4}_[0-9a-fA-F]{4}_[0-9a-fA-F]{12})\.slice$`)

// podCgroupCgroupfs matches the older cgroupfs driver's naming, e.g.
// "kubepods/burstable/pod12345678-1234-1234-1234-123456789012" (guaranteed:
// "kubepods/pod<uid>"). Captures the dashed UID directly.
var podCgroupCgroupfs = regexp.MustCompile(`kubepods/(?:(?:burstable|besteffort)/)?pod([0-9a-fA-F-]{36})$`)

// podCgroup is one discovered pod cgroup directory.
type podCgroup struct {
	uid  string // canonical dashed UUID form, matches the kubelet /pods UID
	path string // absolute path to the cgroup directory (contains cpu.stat)
}

// discoverPodCgroups walks the cgroup v2 tree once and returns every
// directory that looks like a pod cgroup, under EITHER the systemd or
// cgroupfs driver naming — walk-and-match is driver-agnostic, so the
// estimator never has to guess or configure which driver a cluster uses.
// Directories with no cpu.stat are skipped (not yet fully created, or not a
// leaf pod cgroup); this is expected churn, not an error. Called on every
// sampler tick (Task 7): pods come and go, so the map cannot be cached
// indefinitely.
func discoverPodCgroups(root string) ([]podCgroup, error) {
	var out []podCgroup
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// A cgroup can disappear between readdir and stat (pod exited
			// mid-walk) — skip it, don't fail the whole walk.
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		uid := ""
		if m := podCgroupSystemd.FindStringSubmatch(path); m != nil {
			uid = strings.ReplaceAll(m[1], "_", "-")
		} else if m := podCgroupCgroupfs.FindStringSubmatch(path); m != nil {
			uid = m[1]
		} else {
			return nil
		}
		if _, err := os.Stat(filepath.Join(path, "cpu.stat")); err != nil {
			return nil
		}
		out = append(out, podCgroup{uid: uid, path: path})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking cgroup root %s: %w", root, err)
	}
	return out, nil
}

// readCPUStatUsage reads cgroup v2 cpu.stat's cumulative usage_usec — the
// same accounting the kubelet itself reads for kubectl top. cgroup v1 has no
// cpu.stat with this field, hence the AEP's documented cgroup-v2-required
// limitation: this returns an error (fails loud) rather than misreading a
// v1 file.
func readCPUStatUsage(cgroupPath string) (uint64, error) {
	f, err := os.Open(filepath.Join(cgroupPath, "cpu.stat"))
	if err != nil {
		return 0, fmt.Errorf("opening cpu.stat at %s: %w", cgroupPath, err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 2 && fields[0] == "usage_usec" {
			v, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				return 0, fmt.Errorf("parsing usage_usec at %s: %w", cgroupPath, err)
			}
			return v, nil
		}
	}
	if err := sc.Err(); err != nil {
		return 0, fmt.Errorf("scanning cpu.stat at %s: %w", cgroupPath, err)
	}
	return 0, fmt.Errorf("no usage_usec field in %s/cpu.stat (cgroup v1 fleets are unsupported — AEP)", cgroupPath)
}
