package api

import (
	"github.com/avuru/avuru-obs/hub/internal/storage"
	"github.com/avuru/avuru-obs/hub/internal/topology"
)

// meshFlow is the OBI-measured side of one proxy: the bytes it actually moved,
// and the health of the connections it moved them over.
//
// Separate from the trace-derived call counts on purpose. Calls and bytes
// answer different questions — a proxy forwarding every request while the links
// under it retransmit is healthy by call count and failing by connection — and
// a row that merged them could not show the disagreement.
type meshFlow struct {
	bytesIn  uint64
	bytesOut uint64
	// measuredBytes distinguishes "OBI saw this proxy move nothing" from "OBI
	// was not watching". Only the first is a number; the second must reach the
	// client as an absent field, or a proxy that has stopped forwarding looks
	// identical to one nobody is measuring.
	measuredBytes bool

	rttMs             float64
	failedConnections uint64
	retransmits       uint64
	measuredHealth    bool
}

// meshFlows aggregates flow edges and connection health per transport workload.
//
// RTT takes the WORST of a proxy's links rather than an average: the question a
// row answers is "is anything wrong here", and averaging a bad link with nine
// good ones is how a bad link stops being visible. Failed connections and
// retransmits are counts of events and so they sum.
func meshFlows(
	cls topology.Classifier,
	flows []storage.ServiceEdge,
	health []storage.NetworkEdgeHealth,
) map[string]*meshFlow {
	out := map[string]*meshFlow{}
	at := func(name string) *meshFlow {
		f, ok := out[name]
		if !ok {
			f = &meshFlow{}
			out[name] = f
		}
		return f
	}

	for _, e := range flows {
		if cls.IsTransport(e.Target) {
			f := at(e.Target)
			f.bytesIn += e.Bytes
			f.measuredBytes = true
		}
		if cls.IsTransport(e.Source) {
			f := at(e.Source)
			f.bytesOut += e.Bytes
			f.measuredBytes = true
		}
	}

	// Both ends: a proxy's connection health is the health of the links it sits
	// on, whichever direction they run.
	for _, h := range health {
		for _, name := range [2]string{h.Source, h.Target} {
			if !cls.IsTransport(name) {
				continue
			}
			f := at(name)
			if h.RTTMs > f.rttMs {
				f.rttMs = h.RTTMs
			}
			f.failedConnections += h.FailedConnections
			f.retransmits += h.Retransmits
			f.measuredHealth = true
		}
	}
	return out
}
