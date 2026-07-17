# Style Guide

The front door to avuru-obs coding conventions. The detail lives in
`agent_docs/`; this file points the way and `make check` enforces it.

| Component | Conventions | Linter |
|---|---|---|
| Go (`hub/`) | [agent_docs/go_style.md](./agent_docs/go_style.md) | `golangci-lint run` (`hub/.golangci.yml`) |
| TS/React (`ui/`) | [agent_docs/ui_patterns.md](./agent_docs/ui_patterns.md) | `npm run lint` |

## General

- Small, focused files (aim < 300 lines); split when a file does too much.
- Match the surrounding code's idiom, naming, and comment density.
- Comments explain **why**, not what.
- Never hand-edit generated code (`*/generated/`, codegen output) — regenerate.
- `make check` is the gate for every component you touch.
