package api

import (
	"fmt"

	"github.com/avuru/avuru-obs/hub/internal/meshconfig"
)

// Kiali's thresholds, kept so the two tools agree on a workload: a fifth of
// requests failing is down, one in a thousand is degraded.
const (
	healthDegradedErrorRate = 0.001
	healthDownErrorRate     = 0.20
)

// The verdict vocabulary is the UI's HealthStatus, shared with every other
// health badge in the product.
const (
	healthHealthy  = "healthy"
	healthDegraded = "degraded"
	healthDown     = "down"
	healthIdle     = "idle"
)

// workloadHealth folds what the pods say and what the traffic says into one
// verdict, and names what decided it. Pods first: a workload with nothing
// running is down whatever its error rate was, and a workload with no pods
// at all is idle rather than broken — a scaled-to-zero Deployment is a
// choice. Then traffic, then the pod count, then silence.
func workloadHealth(wl meshconfig.Workload, row meshWorkloadDTO) meshHealthDTO {
	errRate := 0.0
	if row.ErrorRate != nil {
		errRate = *row.ErrorRate
	}
	switch {
	case wl.Pods == 0:
		return meshHealthDTO{Status: healthIdle, Reason: "no pods"}
	case wl.RunningPods == 0:
		return meshHealthDTO{Status: healthDown, Reason: fmt.Sprintf("none of %d pods is running", wl.Pods)}
	case row.HasTraffic && errRate >= healthDownErrorRate:
		return meshHealthDTO{Status: healthDown, Reason: fmt.Sprintf("%.0f%% of requests failed in the window", errRate*100)}
	case wl.RunningPods < wl.Pods:
		return meshHealthDTO{Status: healthDegraded, Reason: fmt.Sprintf("%d of %d pods running", wl.RunningPods, wl.Pods)}
	case row.HasTraffic && errRate >= healthDegradedErrorRate:
		return meshHealthDTO{Status: healthDegraded, Reason: fmt.Sprintf("%.1f%% of requests failed in the window", errRate*100)}
	case !row.HasTraffic:
		return meshHealthDTO{Status: healthIdle, Reason: "running, no traffic in the window"}
	}
	return meshHealthDTO{Status: healthHealthy, Reason: "every pod running, no failed requests in the window"}
}
