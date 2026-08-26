package api

import (
	"math"
	"net/http"

	"github.com/avuru/avuru-obs/hub/internal/auth"
	"github.com/avuru/avuru-obs/hub/internal/storage"
)

// CostRates are the prices an operator declared in the chart, per hour of
// RESERVED capacity. Zero means "not configured", which is a real answer: this
// product makes no outbound call, so there is no pricing API to fall back on
// and a made-up number would be worse than none.
type CostRates struct {
	CPUCoreHour float64
	MemGiBHour  float64
	Currency    string
}

const bytesPerGiB = 1024 * 1024 * 1024

// priced reports whether money can be shown at all. Both rates must be set:
// costing CPU while treating memory as free would rank the wrong workloads
// first, which is the only thing this screen is for.
func (r CostRates) priced() bool { return r.CPUCoreHour > 0 && r.MemGiBHour > 0 }

// hourly turns reserved capacity into currency per hour.
func (r CostRates) hourly(cores, memBytes float64) float64 {
	return cores*r.CPUCoreHour + memBytes/bytesPerGiB*r.MemGiBHour
}

// workloadCostDTO is one workload's reservation against its usage.
//
// requestsNothing is not derived by the client from a zero: a workload with no
// declared request and a workload whose request is genuinely tiny are
// different problems, and only one of them is unschedulable by accident and
// first in line for eviction.
type workloadCostDTO struct {
	Workload         string  `json:"workload"`
	Namespace        string  `json:"namespace"`
	Pods             uint64  `json:"pods"`
	RequestsNothing  bool    `json:"requestsNothing"`
	ReservedCPUCores float64 `json:"reservedCpuCores"`
	ReservedMemBytes float64 `json:"reservedMemBytes"`
	UsedCPUCoresPeak float64 `json:"usedCpuCoresPeak"`
	UsedCPUCoresMean float64 `json:"usedCpuCoresMean"`
	UsedMemBytesPeak float64 `json:"usedMemBytesPeak"`
	UsedMemBytesMean float64 `json:"usedMemBytesMean"`
	// Idle capacity: reserved minus the PEAK, never minus the mean. A request
	// cannot be cut below the peak without risking eviction, so the peak is
	// what bounds the reclaimable amount — subtracting the mean would report
	// as waste capacity the workload demonstrably needed.
	IdleCPUCores float64 `json:"idleCpuCores"`
	IdleMemBytes float64 `json:"idleMemBytes"`
	// Money, present only when both rates are configured.
	ReservedCostPerHour *float64 `json:"reservedCostPerHour,omitempty"`
	IdleCostPerHour     *float64 `json:"idleCostPerHour,omitempty"`
}

type nodeCostDTO struct {
	Node                string  `json:"node"`
	AllocatableCPUCores float64 `json:"allocatableCpuCores"`
	AllocatableMemBytes float64 `json:"allocatableMemBytes"`
	RequestedCPUCores   float64 `json:"requestedCpuCores"`
	RequestedMemBytes   float64 `json:"requestedMemBytes"`
	UsedCPUCores        float64 `json:"usedCpuCores"`
	UsedMemBytes        float64 `json:"usedMemBytes"`
}

// costResponse leads with `priced` for the same reason the mesh control-plane
// response leads with `available`: every money field after it is absent when
// it is false, and a client that assumed otherwise would render zeros as
// though a workload were free.
type costResponse struct {
	Priced    bool              `json:"priced"`
	Currency  string            `json:"currency,omitempty"`
	Workloads []workloadCostDTO `json:"workloads"`
}

type nodeCostResponse struct {
	Nodes []nodeCostDTO `json:"nodes"`
}

// handleCostWorkloads ranks workloads by the capacity they reserved and did
// not use.
func (a *API) handleCostWorkloads(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	tr, err := parseTimeRange(r)
	if err != nil {
		return err
	}
	tenant, tenants, err := a.projectTenants(r, auth.RoleViewer)
	if err != nil {
		return err
	}
	limit, err := parseInt(r, "limit", 0)
	if err != nil {
		return err
	}
	rows, err := store.WorkloadCosts(r.Context(), storage.CostQuery{
		Tenant: tenant, Tenants: tenants, Range: tr, Limit: limit,
	})
	if err != nil {
		return err
	}

	rates := a.cfg.CostRates
	resp := costResponse{Priced: rates.priced(), Workloads: []workloadCostDTO{}}
	if resp.Priced {
		resp.Currency = rates.Currency
	}
	for _, c := range rows {
		idleCPU := math.Max(c.ReservedCPUCores-c.UsedCPUCoresPeak, 0)
		idleMem := math.Max(c.ReservedMemBytes-c.UsedMemBytesPeak, 0)
		d := workloadCostDTO{
			Workload:         c.Workload,
			Namespace:        c.Namespace,
			Pods:             c.Pods,
			RequestsNothing:  c.ReservedCPUCores == 0 && c.ReservedMemBytes == 0,
			ReservedCPUCores: c.ReservedCPUCores,
			ReservedMemBytes: c.ReservedMemBytes,
			UsedCPUCoresPeak: c.UsedCPUCoresPeak,
			UsedCPUCoresMean: c.UsedCPUCoresMean,
			UsedMemBytesPeak: c.UsedMemBytesPeak,
			UsedMemBytesMean: c.UsedMemBytesMean,
			IdleCPUCores:     idleCPU,
			IdleMemBytes:     idleMem,
		}
		if resp.Priced {
			reserved := rates.hourly(c.ReservedCPUCores, c.ReservedMemBytes)
			idle := rates.hourly(idleCPU, idleMem)
			d.ReservedCostPerHour = &reserved
			d.IdleCostPerHour = &idle
		}
		resp.Workloads = append(resp.Workloads, d)
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}

// handleCostNodes reports allocatable capacity against what requests have
// claimed and what is actually in use.
func (a *API) handleCostNodes(w http.ResponseWriter, r *http.Request) error {
	store, err := a.store()
	if err != nil {
		return err
	}
	tr, err := parseTimeRange(r)
	if err != nil {
		return err
	}
	tenant, tenants, err := a.projectTenants(r, auth.RoleViewer)
	if err != nil {
		return err
	}
	rows, err := store.NodeCosts(r.Context(), storage.CostQuery{Tenant: tenant, Tenants: tenants, Range: tr})
	if err != nil {
		return err
	}
	resp := nodeCostResponse{Nodes: []nodeCostDTO{}}
	for _, n := range rows {
		resp.Nodes = append(resp.Nodes, nodeCostDTO{
			Node:                n.Node,
			AllocatableCPUCores: n.AllocatableCPUCores,
			AllocatableMemBytes: n.AllocatableMemBytes,
			RequestedCPUCores:   n.RequestedCPUCores,
			RequestedMemBytes:   n.RequestedMemBytes,
			UsedCPUCores:        n.UsedCPUCores,
			UsedMemBytes:        n.UsedMemBytes,
		})
	}
	writeJSON(w, http.StatusOK, resp)
	return nil
}
