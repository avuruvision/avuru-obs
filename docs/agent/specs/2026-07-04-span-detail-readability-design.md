# Span detail panel: readability, resize, and expand

**Date:** 2026-07-04
**Status:** Approved
**Scope:** `ui/` — Traces screen, span detail side panel

## Problem

Selecting a span in the trace waterfall opens a fixed 384px side panel whose
attribute values can render one character per line, unreadable.

Root cause: in `ui/src/components/traces/span-detail.tsx`, the attribute table
uses `grid-cols-[minmax(140px,auto)_1fr]`. With `auto` as the max, a long
attribute key (e.g. `http.response_content_length_uncompressed`) grows the key
column to its full no-wrap width and squeezes the `1fr` value column to ~1
character. Compounding factors: the panel width is hardcoded (`w-96`), the
body has `px-9` padding (72px lost), and there is no resize or expand
affordance.

## Design

### 1. Readability fix (`span-detail.tsx`)

- Attribute grid becomes `grid-cols-[minmax(120px,45%)_1fr]` so the key column
  can never crush the value column.
- Keys wrap (`break-all`) instead of truncating, so full attribute names stay
  visible.
- Body padding drops from `px-9` to `px-4`.
- Per-value click-to-copy: hovering a value row reveals a small copy icon;
  clicking copies the raw value and flashes a check for ~1.5s (same pattern as
  `views/trace-json.tsx`). Keys get no copy affordance.

### 2. Resizable panel (`trace-detail-panel.tsx`)

- The span-detail `aside` width is driven by a new `useLocalStorageNumber`
  hook (sibling of `useLocalStorageFlag`, hydration-safe via
  `useSyncExternalStore`), key `avuru-span-detail-width`, default 384.
- A 6px drag handle on the panel's left edge (`cursor-col-resize`) resizes
  with pointer capture. Width is clamped to [320px, 70% of the workspace
  container], measured from the container's bounding rect during drag.
- Accessibility: the handle is `role="separator"` with
  `aria-orientation="vertical"`; when focused, ArrowLeft/ArrowRight nudge the
  width by 16px.
- Double-clicking the handle resets the width to the 384px default.
- Text selection is suppressed during drag.

### 3. Expand overlay

- A `Maximize2` icon button in the span-detail header (next to the close
  button, aria-label "Expand span detail") opens the same `SpanDetail`
  content in a centered overlay rendered through `createPortal`:
  `min(1100px, 92vw)` wide, `max-h-[85vh]`, scrollable, dimmed backdrop.
- Backdrop click or Esc closes it. The overlay's Esc listener runs in the
  **capture phase and stops propagation**, because the trace fullscreen mode
  (`trace-workspace.tsx`) already listens for Esc on `document` — without
  this, Esc would close the overlay and exit fullscreen at once.
- Overlay open/closed is local React state, not URL state: it is an ephemeral
  zoom, and keeping it out of the URL keeps deep links clean.

### 4. Testing

Extend `ui/e2e/traces.spec.ts`:

- Select a span with long attribute values; assert values occupy real width
  (no one-character-per-line rendering).
- Drag the resize handle; assert the panel widens and the width survives a
  reload (localStorage persistence).
- Open the expand overlay; assert content is visible; close via Esc and
  backdrop.
- In fullscreen mode, Esc with the overlay open closes only the overlay, not
  fullscreen.

## Out of scope (YAGNI)

- Bottom-drawer layout for span details.
- JSON tab / raw view inside the expand overlay.
- Per-trace or per-service width memory (one global width is enough).
- URL state for the overlay.

## Files touched

| File | Change |
|---|---|
| `ui/src/components/traces/span-detail.tsx` | grid fix, key wrap, padding, click-to-copy |
| `ui/src/components/traces/trace-detail-panel.tsx` | resizable aside, drag handle, expand button + overlay |
| `ui/src/hooks/use-local-storage-number.ts` | new hook (mirrors `use-local-storage-flag.ts`) |
| `ui/e2e/traces.spec.ts` | new scenarios |
