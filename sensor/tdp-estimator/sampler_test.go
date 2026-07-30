package main

import (
	"os"
	"path/filepath"
	"testing"
)

func writeProcStat(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "stat")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadCPUTimes(t *testing.T) {
	path := writeProcStat(t, `cpu  100 0 50 800 10 0 0 0 0 0
cpu0 50 0 25 400 5 0 0 0 0 0
intr 12345
`)
	got, err := readCPUTimes(path)
	if err != nil {
		t.Fatalf("readCPUTimes: %v", err)
	}
	want := cpuTimes{User: 100, Nice: 0, System: 50, Idle: 800, IOWait: 10, IRQ: 0, SoftIRQ: 0, Steal: 0}
	if got != want {
		t.Errorf("readCPUTimes = %+v, want %+v", got, want)
	}
}

func TestReadCPUTimes_NoAggregateLine(t *testing.T) {
	path := writeProcStat(t, "intr 12345\n")
	if _, err := readCPUTimes(path); err == nil {
		t.Error("readCPUTimes: want error when no aggregate \"cpu \" line exists")
	}
}

func TestUtilizationDelta(t *testing.T) {
	tests := []struct {
		name       string
		prev, cur  cpuTimes
		wantApprox float64
	}{
		{
			name:       "50% busy",
			prev:       cpuTimes{User: 100, Idle: 100},
			cur:        cpuTimes{User: 150, Idle: 150}, // +50 user, +50 idle -> 50% util
			wantApprox: 0.5,
		},
		{
			name:       "fully idle",
			prev:       cpuTimes{User: 100, Idle: 100},
			cur:        cpuTimes{User: 100, Idle: 200},
			wantApprox: 0,
		},
		{
			name:       "fully busy",
			prev:       cpuTimes{User: 100, Idle: 100},
			cur:        cpuTimes{User: 200, Idle: 100},
			wantApprox: 1,
		},
		{
			name:       "no time elapsed",
			prev:       cpuTimes{User: 100, Idle: 100},
			cur:        cpuTimes{User: 100, Idle: 100},
			wantApprox: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := utilizationDelta(tt.prev, tt.cur)
			if diff := got - tt.wantApprox; diff > 1e-9 || diff < -1e-9 {
				t.Errorf("utilizationDelta = %v, want %v", got, tt.wantApprox)
			}
		})
	}
}
