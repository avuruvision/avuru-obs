package main

import (
	"os"
	"path/filepath"
	"testing"
)

// makeCgroupFixture builds a fake cgroup v2 tree with one pod directory
// (systemd-driver naming) containing a cpu.stat file, mimicking
// /sys/fs/cgroup/kubepods.slice/kubepods-burstable.slice/kubepods-burstable-pod<uid>.slice/.
func makeCgroupFixture(t *testing.T, uidUnderscored string, usageUsec uint64) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "kubepods.slice", "kubepods-burstable.slice",
		"kubepods-burstable-pod"+uidUnderscored+".slice")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "usage_usec " + itoa(usageUsec) + "\nuser_usec 0\nsystem_usec 0\n"
	if err := os.WriteFile(filepath.Join(dir, "cpu.stat"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

func itoa(v uint64) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}

func TestDiscoverPodCgroups(t *testing.T) {
	root := makeCgroupFixture(t, "12345678_1234_1234_1234_123456789012", 5_000_000)

	cgroups, err := discoverPodCgroups(root)
	if err != nil {
		t.Fatalf("discoverPodCgroups: %v", err)
	}
	if len(cgroups) != 1 {
		t.Fatalf("len(cgroups) = %d, want 1", len(cgroups))
	}
	if cgroups[0].uid != "12345678-1234-1234-1234-123456789012" {
		t.Errorf("uid = %q, want canonical dashed form", cgroups[0].uid)
	}
}

func TestReadCPUStatUsage(t *testing.T) {
	root := makeCgroupFixture(t, "aaaaaaaa_bbbb_cccc_dddd_eeeeeeeeeeee", 7_500_000)
	cgroups, err := discoverPodCgroups(root)
	if err != nil {
		t.Fatalf("discoverPodCgroups: %v", err)
	}
	usage, err := readCPUStatUsage(cgroups[0].path)
	if err != nil {
		t.Fatalf("readCPUStatUsage: %v", err)
	}
	if usage != 7_500_000 {
		t.Errorf("usage = %d, want 7500000 usec", usage)
	}
}
