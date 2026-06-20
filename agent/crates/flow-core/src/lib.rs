//! Pure flow-aggregation logic for the service map.
//!
//! The eBPF side (crates/agent-ebpf, M2) emits raw [`FlowEvent`]s from TCP
//! `connect()`/`listen()` tracing; this crate rolls them up into
//! [`FlowStats`] keyed by [`FlowKey`], ready for OTLP export. Keep this crate
//! free of eBPF, tokio, and OS-specific code: it is the unit-testable heart
//! of the agent (see agent_docs/rust_style.md).

use std::collections::HashMap;
use std::net::IpAddr;

/// Direction of an observed TCP flow relative to the instrumented node.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
pub enum Direction {
    /// Local process initiated the connection (`connect`).
    Outbound,
    /// Local process accepted the connection (`accept` on a `listen` socket).
    Inbound,
}

/// Identity of one edge in the service map, before K8s metadata enrichment.
#[derive(Debug, Clone, PartialEq, Eq, Hash)]
pub struct FlowKey {
    pub src: IpAddr,
    pub dst: IpAddr,
    pub dst_port: u16,
    pub direction: Direction,
}

/// One raw event from the eBPF ring buffer.
#[derive(Debug, Clone)]
pub struct FlowEvent {
    pub key: FlowKey,
    pub bytes: u64,
    /// Connection failed (RST/timeout) — feeds the edge error rate.
    pub failed: bool,
}

/// Aggregated statistics for one flow key within a window.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct FlowStats {
    pub connections: u64,
    pub failures: u64,
    pub bytes: u64,
}

/// Rolls up raw flow events into per-key stats over a reporting window.
///
/// The ring-buffer consumer calls [`record`](Self::record) per event (no
/// allocation on the happy path once the map is warm) and
/// [`drain`](Self::drain) once per export interval.
#[derive(Debug, Default)]
pub struct Aggregator {
    stats: HashMap<FlowKey, FlowStats>,
}

impl Aggregator {
    pub fn new() -> Self {
        Self::default()
    }

    pub fn record(&mut self, event: &FlowEvent) {
        let entry = self.stats.entry(event.key.clone()).or_default();
        entry.connections += 1;
        entry.bytes += event.bytes;
        if event.failed {
            entry.failures += 1;
        }
    }

    /// Returns the accumulated stats and resets the window.
    pub fn drain(&mut self) -> HashMap<FlowKey, FlowStats> {
        std::mem::take(&mut self.stats)
    }

    pub fn is_empty(&self) -> bool {
        self.stats.is_empty()
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::net::Ipv4Addr;

    fn key(port: u16) -> FlowKey {
        FlowKey {
            src: IpAddr::V4(Ipv4Addr::new(10, 0, 0, 1)),
            dst: IpAddr::V4(Ipv4Addr::new(10, 0, 0, 2)),
            dst_port: port,
            direction: Direction::Outbound,
        }
    }

    #[test]
    fn aggregates_events_by_key() {
        let mut agg = Aggregator::new();
        agg.record(&FlowEvent { key: key(8080), bytes: 100, failed: false });
        agg.record(&FlowEvent { key: key(8080), bytes: 50, failed: true });
        agg.record(&FlowEvent { key: key(9090), bytes: 10, failed: false });

        let stats = agg.drain();
        assert_eq!(stats.len(), 2);
        let s = &stats[&key(8080)];
        assert_eq!(s.connections, 2);
        assert_eq!(s.failures, 1);
        assert_eq!(s.bytes, 150);
    }

    #[test]
    fn drain_resets_the_window() {
        let mut agg = Aggregator::new();
        agg.record(&FlowEvent { key: key(8080), bytes: 1, failed: false });
        assert!(!agg.is_empty());
        let _ = agg.drain();
        assert!(agg.is_empty());
        assert!(agg.drain().is_empty());
    }
}
