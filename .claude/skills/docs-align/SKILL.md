---
name: docs-align
description: |
  Keep the avuru-obs-docs site aligned with the engine. Use whenever an
  avuru-obs feature merges to main, a milestone (M1–M5) completes, a hub API
  endpoint/param changes, or a release is cut — BEFORE claiming that work done.
  Produces bilingual (EN + FR) updates: changelog entry, feature-status matrix,
  roadmap badges, API reference, release timeline.
---

# docs-align — keep avuru-obs-docs in lockstep with the engine

The docs site is a **separate repo**: sibling checkout at `../avuru-obs-docs`
(GitHub `avuruvision/avuru-obs-doc`, Docusaurus 3, EN default + full FR locale).
Verify the checkout exists first; if it moved, ask rather than guess.
Deploys are automatic on merge to its `main` (FTPS → Hostinger) — never deploy
manually. Work on a `docs/<topic>` branch and open a PR.

## What to update (per trigger)

| Trigger | Required updates |
|---|---|
| Feature merged to engine main | Changelog entry (EN+FR) + status matrix (EN+FR) |
| Milestone flips (M1–M5) | Above + roadmap milestone badge (EN+FR) |
| Hub API endpoint/param change | `reference/api.mdx` (align to `hub/internal/api/router.go`) |
| Release cut (`vX.Y.Z`) | Above + `docs/releases.mdx` ReleaseEntry (EN+FR) + release changelog entry |

## 1. Changelog entry (bilingual, always)

Create **both** files with identical frontmatter (title translated):

- EN: `changelog/YYYY-MM-DD-<slug>.mdx`
- FR: `i18n/fr/docusaurus-plugin-content-blog-changelog/YYYY-MM-DD-<slug>.mdx`

```mdx
---
slug: <slug>
title: "<Action + user benefit>"
authors: [avuru]
tags: [<from changelog/tags.yml: traces | service-map | logs | ui | platform | docs>]
date: YYYY-MM-DD
---

One-sentence teaser naming the user-facing change.

{/* truncate */}

- **Bold subheading.** Detail with context; link related docs pages.
```

New tag categories go in `changelog/tags.yml` (shared, not translated), authors
in `changelog/authors.yml`.

## 2. Feature-status matrix

- EN: `docs/status.mdx` — move the capability from "Coming" → "Available now",
  set the Since/Target column (M1…M5), `<StatusBadge status="shipped" />`.
- FR: `i18n/fr/docusaurus-plugin-content-docs/current/status.mdx` — same
  structure; badges take `label="Livré"` / `"En cours"` / `"Prévu"`.

## 3. Roadmap

- EN: `docs/roadmap.mdx` — flip `<MilestoneCard tag="Mx" status="shipped">`
  when a milestone completes.
- FR mirror: same path under `i18n/fr/docusaurus-plugin-content-docs/current/`;
  add `statusLabel="Livré"` etc.

## 4. API reference

`reference/api.mdx`: keep the endpoint table matching
`avuru-obs/hub/internal/api/router.go` (method, path, params, purpose).
Document deliberate exceptions (e.g. OTLP ingest paths outside `/api/v1`) with
their rationale.

## 5. Release timeline (release cuts only)

`docs/releases.mdx` + FR mirror: add a `<ReleaseEntry version="X.Y.Z"
date="…" status="shipped">` inside `<Timeline>`; link the GitHub release and
the matching changelog entry.

## Writing rules

- **No competitor comparisons or names** (repo rule; see the site's
  no-competitor writing rule).
- FR is a **real translation**, never a copy of the English or a stub.
- Conventional commits, `docs:` prefix; **no AI co-author trailers**.
- Marketing tone in changelog titles (action + benefit), factual body.

## Validation (must pass before PR)

```bash
cd ../avuru-obs-docs
npm run lint:docs    # frontmatter (includes changelog/)
npm run typecheck    # component props
npm run build        # all locales; broken links throw
```

Optional visual check: `npm start` (EN) / `npm run start:fr` (FR) — verify
`/changelog`, `/docs/status`, `/docs/roadmap`, `/docs/releases` in both locales.

## Definition of done

- [ ] Changelog EN + FR created, same slug/date/tags
- [ ] status.mdx EN + FR reflect the new reality
- [ ] roadmap.mdx EN + FR badge flipped (if milestone changed)
- [ ] reference/api.mdx matches router.go (if API changed)
- [ ] releases.mdx EN + FR updated (if release cut)
- [ ] lint:docs + typecheck + build green
- [ ] PR opened on avuru-obs-doc (deploy is automatic on merge)
