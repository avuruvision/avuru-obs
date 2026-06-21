# Maintainers

Maintainers have merge rights and are accountable for the health of avuru-obs.
See [GOVERNANCE.md](GOVERNANCE.md) for what the role means and how it is granted.
This file is the source of truth for [`.github/CODEOWNERS`](.github/CODEOWNERS) —
keep the two in sync.

## Active maintainers

| Name | GitHub | Areas | Contact |
|---|---|---|---|
| Berny | [@egilberny](https://github.com/egilberny) | all components, governance, release | egilberny@lab.luxavuru.com |

> Keep this table current. When a maintainer is added, update the row here **and**
> the relevant lines in [`.github/CODEOWNERS`](.github/CODEOWNERS) in the same PR.
> GitHub handles must belong to org members for CODEOWNERS routing to work.

## Component owners

Until there are more maintainers, every area below is owned by the active
maintainer(s) above. As the team grows, split ownership here and mirror it in
CODEOWNERS so reviews route automatically.

| Area | Path(s) | Owner |
|---|---|---|
| Agent (Rust / eBPF) | `agent/` | @egilberny |
| Hub (Go) | `hub/` | @egilberny |
| UI (Next.js / TS) | `ui/` | @egilberny |
| Gateway / schemas | `gateway/` | @egilberny |
| Contracts | `proto/` | @egilberny |
| Deploy (Helm / compose) | `deploy/`, `sensor/` | @egilberny |
| Docs & governance | `agent_docs/`, `design/`, root `*.md` | @egilberny |

## Emeritus

_None yet._ Former maintainers who have stepped down are listed here — see
[GOVERNANCE.md § Stepping down](GOVERNANCE.md#stepping-down).
