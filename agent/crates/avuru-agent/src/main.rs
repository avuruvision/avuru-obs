//! avuru-agent: node agent tracing TCP connect/listen via eBPF to feed the
//! service map. M0 scaffold — the aya-based tracer and OTLP exporter land in
//! M2 (see agent_docs/architecture.md).

fn main() -> anyhow::Result<()> {
    println!("avuru-agent {} (M0 scaffold)", env!("CARGO_PKG_VERSION"));

    #[cfg(not(target_os = "linux"))]
    {
        eprintln!("eBPF flow tracing requires Linux; this build is check-only.");
    }

    #[cfg(target_os = "linux")]
    {
        // M2: load eBPF programs (aya), consume the ring buffer into
        // flow_core::Aggregator, export over OTLP every interval.
    }

    Ok(())
}
