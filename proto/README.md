# proto/ — shared contracts

Single source of truth for structures crossing language boundaries
(Rust agent ↔ Go hub/gateway ↔ TS UI). Read
[`agent_docs/proto_contracts.md`](../agent_docs/proto_contracts.md) before
changing anything here.

Codegen (`make proto`, buf-based) is wired in M1; until then this directory
defines the contracts the M1/M2 implementations must generate from.
