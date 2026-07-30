package main

import "testing"

func TestNodePower(t *testing.T) {
	c := Coefficients{IdleWatts: 10, MaxWatts: 50}
	tests := []struct {
		util float64
		want float64
	}{
		{0, 10},  // fully idle -> P_idle
		{1, 50},  // fully busy -> P_max
		{0.5, 30}, // halfway -> midpoint
	}
	for _, tt := range tests {
		if got := nodePower(c, tt.util); got != tt.want {
			t.Errorf("nodePower(util=%v) = %v, want %v", tt.util, got, tt.want)
		}
	}
}

func TestIntegrateJoules(t *testing.T) {
	// Two samples 10s apart at constant 20W -> 200 joules (trapezoidal of a
	// flat line is exact).
	samples := []wattSample{
		{atSeconds: 0, watts: 20},
		{atSeconds: 10, watts: 20},
	}
	got := integrateJoules(samples)
	if got != 200 {
		t.Errorf("integrateJoules = %v, want 200", got)
	}
}

func TestIntegrateJoules_RampAndGap(t *testing.T) {
	// 0s@10W -> 10s@30W (trapezoid: (10+30)/2 * 10 = 200J) then a 50s GAP
	// (sensor missed samples) -> 60s@30W. The gap must not be integrated as
	// if power were held constant for 50s at some interpolated value beyond
	// what was actually observed at the gap's start; trapezoidal integration
	// naturally handles this correctly using only the two straddling samples.
	samples := []wattSample{
		{atSeconds: 0, watts: 10},
		{atSeconds: 10, watts: 30},
		{atSeconds: 60, watts: 30},
	}
	got := integrateJoules(samples)
	want := 200.0 + (30.0+30.0)/2*50 // 200 + 1500 = 1700
	if got != want {
		t.Errorf("integrateJoules = %v, want %v", got, want)
	}
}

func TestIntegrateJoules_SingleSample(t *testing.T) {
	if got := integrateJoules([]wattSample{{atSeconds: 0, watts: 20}}); got != 0 {
		t.Errorf("integrateJoules(1 sample) = %v, want 0 (no interval to integrate over)", got)
	}
}

func TestPodDynamicShare(t *testing.T) {
	// Node used 8 of its 10s window busy (80% util); pod used 2s of CPU time
	// in that window -> pod's share of the node's ACTIVE (non-idle) time is
	// 2/8 = 0.25, applied only to the dynamic (P-P_idle) portion.
	nodeCoeff := Coefficients{IdleWatts: 10, MaxWatts: 50}
	nodePowerW := nodePower(nodeCoeff, 0.8) // 42W
	podW := podDynamicPower(nodePowerW, nodeCoeff.IdleWatts, podShareOfActive(2, 8))
	// dynamic = 42-10 = 32W; pod share 0.25 -> 8W
	if podW != 8 {
		t.Errorf("podDynamicPower = %v, want 8", podW)
	}
}

func TestPodShareOfActive_NoActiveTime(t *testing.T) {
	if got := podShareOfActive(2, 0); got != 0 {
		t.Errorf("podShareOfActive with 0 node-active-seconds = %v, want 0 (guards div-by-zero)", got)
	}
}
