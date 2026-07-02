package api

import (
	"net/http"
	"time"

	"github.com/avuru/avuru-obs/hub/internal/storage"
)

type metricPointDTO struct {
	Time  string  `json:"time"`
	Value float64 `json:"value"`
}

type nodeDTO struct {
	Name                 string           `json:"name"`
	CPUUsageCores        float64          `json:"cpuUsageCores"`
	MemoryUsageBytes     uint64           `json:"memoryUsageBytes"`
	MemoryAvailableBytes uint64           `json:"memoryAvailableBytes"`
	NetworkRxBytesPerSec float64          `json:"networkRxBytesPerSec"`
	NetworkTxBytesPerSec float64          `json:"networkTxBytesPerSec"`
	PodCount             uint64           `json:"podCount"`
	CPUSeries            []metricPointDTO `json:"cpuSeries"`
	MemorySeries         []metricPointDTO `json:"memorySeries"`
}

type nodesResponse struct {
	Nodes []nodeDTO `json:"nodes"`
}

type podDTO struct {
	Name             string  `json:"name"`
	Namespace        string  `json:"namespace"`
	Node             string  `json:"node"`
	Workload         string  `json:"workload,omitempty"`
	CPUUsageCores    float64 `json:"cpuUsageCores"`
	MemoryUsageBytes uint64  `json:"memoryUsageBytes"`
}

type podsResponse struct {
	Pods []podDTO `json:"pods"`
}

func toPoints(pts []storage.MetricPoint) []metricPointDTO {
	out := make([]metricPointDTO, 0, len(pts))
	for _, p := range pts {
		out = append(out, metricPointDTO{Time: p.Time.UTC().Format(time.RFC3339), Value: p.Value})
	}
	return out
}

// handleInfraNodes serves node utilization (kubeletstats via the sensor).
func (a *API) handleInfraNodes(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	tr, err := parseTimeRange(r)
	if err != nil {
		return err
	}
	points, err := parseInt(r, "points", 0)
	if err != nil {
		return err
	}
	nodes, err := store.ListNodeStats(r.Context(), storage.InfraQuery{
		Tenant: tenant(r),
		Range:  tr,
		Points: points,
	})
	if err != nil {
		return err
	}
	resp := nodesResponse{Nodes: make([]nodeDTO, 0, len(nodes))}
	for _, n := range nodes {
		resp.Nodes = append(resp.Nodes, nodeDTO{
			Name:                 n.Name,
			CPUUsageCores:        n.CPUUsage,
			MemoryUsageBytes:     n.MemoryUsage,
			MemoryAvailableBytes: n.MemoryAvailable,
			NetworkRxBytesPerSec: n.NetworkRxRate,
			NetworkTxBytesPerSec: n.NetworkTxRate,
			PodCount:             n.PodCount,
			CPUSeries:            toPoints(n.CPUSeries),
			MemorySeries:         toPoints(n.MemorySeries),
		})
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// handleInfraPods serves per-pod utilization, optionally for one node.
func (a *API) handleInfraPods(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	tr, err := parseTimeRange(r)
	if err != nil {
		return err
	}
	limit, err := parseInt(r, "limit", 0)
	if err != nil {
		return err
	}
	pods, err := store.ListPodStats(r.Context(), storage.InfraQuery{
		Tenant: tenant(r),
		Range:  tr,
		Node:   r.URL.Query().Get("node"),
		Limit:  limit,
	})
	if err != nil {
		return err
	}
	resp := podsResponse{Pods: make([]podDTO, 0, len(pods))}
	for _, p := range pods {
		resp.Pods = append(resp.Pods, podDTO{
			Name:             p.Name,
			Namespace:        p.Namespace,
			Node:             p.Node,
			Workload:         p.Workload,
			CPUUsageCores:    p.CPUUsage,
			MemoryUsageBytes: p.MemoryUsage,
		})
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}
