# agent_docs/

Topic documentation for AI agents (and humans), loaded via progressive
disclosure: read [`AGENTS.md`](../AGENTS.md) first, then pull only the topic
file relevant to your task.

| File | Read when |
|---|---|
| `architecture.md` | starting any task — data flow, locked decisions, rationale |
| `tech_stack.md` | adding/upgrading a dependency or touching pinned components |
| `development.md` | setting up, running, or wiring components together |
| `testing.md` | writing or running any test |
| `proto_contracts.md` | before touching `proto/` or any generated code |
| `go_style.md` | actively writing Go (hub) |
| `rust_style.md` | actively writing Rust (agent) |
| `ui_patterns.md` | actively writing UI code |

Keep these files current: a PR that changes an architectural decision, a
pinned version, a port, or a workflow updates the matching doc in the same PR.
