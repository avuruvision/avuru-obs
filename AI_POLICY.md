# Avuru Obs — AI Policy

Guidelines for using AI/ML tools (Claude, Copilot, Cursor, ChatGPT, …) when
contributing to avuru-obs. Adapted from the Kiali project's AI policy; trimmed
to a pragmatic subset for this project's stage. Read alongside
[AGENTS.md](./AGENTS.md), [CONTRIBUTING.md](./CONTRIBUTING.md), and
[STYLE_GUIDE.md](./STYLE_GUIDE.md).

## Core principles

1. **Human accountability.** Every contribution must be reviewed, understood,
   and validated by a human. The contributor is fully responsible for the
   quality, security, and correctness of submitted code — regardless of how it
   was produced. AI is an assistant, not a replacement for judgment.
2. **Quality & security first.** All code meets the project's standards and
   passes `make check`. Security takes precedence over speed.
3. **Open source values.** Contributions align with the project [LICENSE](./LICENSE);
   be transparent about how work was produced.

## Permitted uses

AI tools may assist with: boilerplate and well-defined implementations,
refactoring, unit tests, documentation drafting and clarity, code review and
bug-finding, debugging, research, and regex/query generation.

## Required practices

When using AI tools, you **MUST**:

1. **Understand the code** — review all AI-generated code before submission; be
   able to explain and defend it in review. You own its content and origin.
2. **Verify license compliance** — ensure the tool's output terms don't conflict
   with the project license; attribute any third-party material the tool surfaces.
3. **Test thoroughly** — AI-assisted code ships with tests that validate it, and
   all of `make check` passes (per-component commands in AGENTS.md).

## Disclosure of AI assistance

> **DEFERRED DECISION.** [AGENTS.md](./AGENTS.md) is the single source of truth
> and currently mandates **NO AI co-author / `Assisted-by:` / `Co-Authored-By:`
> trailers in commit history** (history was rewritten once to enforce this).
> This policy therefore does **not** require disclosure trailers. The team will
> revisit whether/how to disclose AI assistance (PR description vs. trailer vs.
> none); until then, follow AGENTS.md. Do not add AI trailers to commits.

Be honest about your process; maintainers may ask about code origin in review.

## Prohibited practices

- **Blind submission** — shipping AI output you can't explain or maintain.
- **License violations** — using AI output that breaks license compatibility or
  includes unattributed copyrighted material.
- **Security negligence** — submitting known-vulnerable code, or AI-generated
  handling of secrets/credentials without careful review.
- **Gaming the system** — bulk low-quality AI submissions to inflate metrics.
- **Misrepresentation** — false attribution or misleading claims about origin.

## Documentation & generated media

AI-assisted docs are encouraged for clarity but must be technically accurate and
human-reviewed; verify examples actually work. Design docs/RFCs (`design/`): the
human author must understand and support the proposal. Technical diagrams from
AI are acceptable if accurate and not otherwise copyrighted; avoid decorative
AI artwork.
