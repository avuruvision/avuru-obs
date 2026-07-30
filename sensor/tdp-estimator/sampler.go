package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// defaultProcStat is the standard /proc/stat path; overridable in tests.
const defaultProcStat = "/proc/stat"

// cpuTimes holds the cumulative jiffy counters from /proc/stat's aggregate
// "cpu" line (all cores summed). Values are cumulative since boot — callers
// difference two samples to get utilization over an interval, never read
// this as an instantaneous percentage.
type cpuTimes struct {
	User, Nice, System, Idle, IOWait, IRQ, SoftIRQ, Steal uint64
}

// total is every counted jiffy — the utilizationDelta denominator.
func (c cpuTimes) total() uint64 {
	return c.User + c.Nice + c.System + c.Idle + c.IOWait + c.IRQ + c.SoftIRQ + c.Steal
}

// idle is time the kernel does not count as CPU work: true idle plus iowait
// (a core waiting on I/O is not doing CPU work either).
func (c cpuTimes) idle() uint64 {
	return c.Idle + c.IOWait
}

// readCPUTimes parses the aggregate "cpu" line (fields documented in
// proc(5)); per-core "cpu0", "cpu1", ... lines are ignored — the model only
// needs whole-node utilization.
func readCPUTimes(path string) (cpuTimes, error) {
	f, err := os.Open(path)
	if err != nil {
		return cpuTimes{}, fmt.Errorf("opening %s: %w", path, err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 9 || fields[0] != "cpu" {
			continue
		}
		vals := make([]uint64, 8)
		for i := 0; i < 8; i++ {
			v, err := strconv.ParseUint(fields[i+1], 10, 64)
			if err != nil {
				return cpuTimes{}, fmt.Errorf("parsing %s field %d: %w", path, i+1, err)
			}
			vals[i] = v
		}
		return cpuTimes{User: vals[0], Nice: vals[1], System: vals[2], Idle: vals[3], IOWait: vals[4], IRQ: vals[5], SoftIRQ: vals[6], Steal: vals[7]}, nil
	}
	if err := sc.Err(); err != nil {
		return cpuTimes{}, fmt.Errorf("scanning %s: %w", path, err)
	}
	return cpuTimes{}, fmt.Errorf("no aggregate \"cpu \" line found in %s", path)
}

// utilizationDelta is fractional node CPU utilization (0..1) between two
// /proc/stat samples. Returns 0 on no elapsed time (guards a div-by-zero on
// a too-fast poll rather than returning NaN/Inf into the power model).
func utilizationDelta(prev, cur cpuTimes) float64 {
	totalDelta := cur.total() - prev.total()
	if totalDelta == 0 {
		return 0
	}
	idleDelta := cur.idle() - prev.idle()
	return 1 - float64(idleDelta)/float64(totalDelta)
}
