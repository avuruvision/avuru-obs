# Span Detail Readability + Resize + Expand Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the trace span-detail side panel readable (fix the grid bug that renders values one character per line), resizable by drag with persisted width, and expandable into a wide overlay.

**Architecture:** Three UI components change in `ui/src/components/traces/`: `span-detail.tsx` gets a capped key column, wrapping keys, and per-value copy; `trace-detail-panel.tsx` gets a drag handle driving a localStorage-persisted width plus an expand button; a new `span-detail-overlay.tsx` renders the same content in a portal above the fullscreen layer. A new `useLocalStorageNumber` hook mirrors the existing `useLocalStorageFlag`. Tests are Playwright e2e only (this repo has no unit-test runner for the UI) against the seeded compose stack.

**Tech Stack:** Next.js 16 (static export), React, Tailwind/daisyUI, lucide-react icons, Playwright e2e, docker compose seeded stack.

**Spec:** `docs/superpowers/specs/2026-07-04-span-detail-readability-design.md`

---

## File structure

| File | Action | Responsibility |
|---|---|---|
| `ui/src/hooks/use-local-storage-number.ts` | Create | Hydration-safe numeric localStorage state (mirrors `use-local-storage-flag.ts`) |
| `ui/src/components/traces/span-detail.tsx` | Modify | Attribute tables: capped key column, wrapping keys, per-value copy |
| `ui/src/components/traces/span-detail-overlay.tsx` | Create | Portal overlay showing `SpanDetail` wide; capture-phase Esc |
| `ui/src/components/traces/trace-detail-panel.tsx` | Modify | Resizable aside (drag handle, clamp, reset), expand button, overlay mount |
| `deploy/compose/seed/fixtures/traces_checkout.json` | Modify | Add one long-key attribute so e2e can reproduce the pre-fix bug |
| `ui/e2e/traces.spec.ts` | Modify | New `span detail panel` describe block (4 tests) |

## Testing model (read first)

The UI has **no unit test runner** — only Playwright e2e against the seeded compose stack. The full lifecycle (`make e2e-ui`) rebuilds everything and tears down, so per-task red/green uses a **dev loop** instead:

```bash
# One-time stack bring-up (leaves it running; hub API exposed on :8080):
docker compose -f deploy/compose/docker-compose.yaml up -d --build --wait clickhouse hub
docker compose -f deploy/compose/docker-compose.yaml up -d --build gateway demo ui
cd tools/seed && go run . -endpoint http://localhost:4318 -fixtures ../../deploy/compose/seed/fixtures && cd ../..

# Dev server (next dev proxies /api -> localhost:8080 per next.config.ts):
cd ui && npm run dev   # serves :3000, run in background

# Run just the new tests against the dev server:
cd ui && AVURUOPS_BASE_URL=http://localhost:3000 npx playwright test -g "span detail panel"
```

Seeding must happen **after** Task 1 edits the fixture. If Docker is unavailable, note it, still write tests first, and defer all runs to the final `make e2e-ui`; do not claim success without that run.

Each code task ends with `npm run lint` and `npm run typecheck` (fast, no stack needed).

---

### Task 1: Seed fixture long-key attribute + failing e2e tests

**Files:**
- Modify: `deploy/compose/seed/fixtures/traces_checkout.json` (SELECT orders span attributes)
- Modify: `ui/e2e/traces.spec.ts` (append describe block)

- [x] **Step 1: Add a long attribute key to the seeded SELECT orders span**

In `traces_checkout.json`, the `SELECT orders` span's `attributes` array becomes:

```json
"attributes": [
  { "key": "db.system.name", "value": { "stringValue": "postgresql" } },
  { "key": "db.query.text", "value": { "stringValue": "SELECT * FROM orders WHERE id = ?" } },
  { "key": "db.client.connections.wait_time_before_timeout_ms", "value": { "intValue": "30000" } }
]
```

The 50-char key reproduces the bug pre-fix: with `minmax(140px,auto)` the key column grows to full no-wrap width and crushes the value column.

- [x] **Step 2: Append the new e2e describe block**

At the end of `ui/e2e/traces.spec.ts` (also change the first import line to `import { test, expect, type Page } from "@playwright/test";`):

```ts
test.describe("span detail panel (seeded data)", () => {
  const LONG_VALUE = "SELECT * FROM orders WHERE id = ?";

  async function openSpanDetail(page: Page) {
    await page.goto(`/traces?trace=${SEED_TRACE_ID}&tab=traces`);
    await page.getByRole("button", { name: /SELECT orders/ }).click();
    await expect(page.getByText("Span detail", { exact: true })).toBeVisible();
  }

  test("attribute values stay readable next to long keys", async ({ page }) => {
    await openSpanDetail(page);
    // The seeded span carries a 50-char key; pre-fix it crushed the value
    // column to ~1 character per line.
    await expect(
      page.getByText("db.client.connections.wait_time_before_timeout_ms"),
    ).toBeVisible();
    const value = page.getByText(LONG_VALUE).first();
    await expect(value).toBeVisible();
    const box = (await value.boundingBox())!;
    expect(box.width).toBeGreaterThan(120);
    expect(box.height).toBeLessThan(60);
  });

  test("attribute value copies to clipboard", async ({ page, context }) => {
    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    await openSpanDetail(page);
    await page.getByText(LONG_VALUE).first().hover();
    await page.getByRole("button", { name: "Copy db.query.text" }).click();
    expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(LONG_VALUE);
  });

  test("panel resizes by drag, persists, and resets on double-click", async ({ page }) => {
    await openSpanDetail(page);
    const aside = page.locator("aside").filter({ hasText: "Span detail" });
    const handle = page.getByRole("separator", { name: "Resize span detail" });

    const before = (await aside.boundingBox())!;
    const h = (await handle.boundingBox())!;
    await page.mouse.move(h.x + h.width / 2, h.y + h.height / 2);
    await page.mouse.down();
    await page.mouse.move(h.x - 150, h.y + h.height / 2, { steps: 5 });
    await page.mouse.up();
    const after = (await aside.boundingBox())!;
    expect(after.width).toBeGreaterThan(before.width + 100);

    await page.reload();
    await expect(page.getByText("Span detail", { exact: true })).toBeVisible();
    const reloaded = (await aside.boundingBox())!;
    expect(Math.abs(reloaded.width - after.width)).toBeLessThan(10);

    await handle.dblclick();
    await expect
      .poll(async () => (await aside.boundingBox())!.width)
      .toBeLessThan(after.width);
  });

  test("expand overlay opens; Esc closes it without leaving fullscreen", async ({ page }) => {
    await openSpanDetail(page);
    await page.getByRole("button", { name: "Full screen" }).click();
    await page.getByRole("button", { name: "Expand span detail" }).click();

    const dialog = page.getByRole("dialog", { name: "Span detail expanded" });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByText(LONG_VALUE)).toBeVisible();

    await page.keyboard.press("Escape");
    await expect(dialog).not.toBeVisible();
    await expect(page.getByRole("button", { name: "Exit full screen" })).toBeVisible();

    await page.keyboard.press("Escape");
    await expect(page.getByRole("button", { name: "Full screen" })).toBeVisible();
  });
});
```

Locator notes: `.first()` on `getByText(LONG_VALUE)` avoids a strict-mode violation (the `dd` and its inner `span` both match). The overlay assertions are scoped to `dialog` because the side panel behind it shows the same text.

- [x] **Step 3: Bring up the stack, seed, start the dev server** (commands in "Testing model" above)

- [x] **Step 4: Run the new tests — expect all 4 to FAIL (red)**

Run: `cd ui && AVURUOPS_BASE_URL=http://localhost:3000 npx playwright test -g "span detail panel"`
Expected: readability test fails on `box.width` (crushed column); the other three fail because the separator, copy button, and expand button don't exist yet.

- [x] **Step 5: Commit the fixture + red tests**

```bash
git add deploy/compose/seed/fixtures/traces_checkout.json ui/e2e/traces.spec.ts
git commit -m "test(e2e): span detail readability, resize, expand scenarios (red)"
```

---

### Task 2: Readability fix + click-to-copy in span-detail.tsx

**Files:**
- Modify: `ui/src/components/traces/span-detail.tsx`

- [x] **Step 1: Rewrite the attribute table with capped key column and copy affordance**

Replace the imports and `AttrTable` in `span-detail.tsx` with:

```tsx
"use client";

import { useState } from "react";
import { Check, Copy } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { formatTime, utcTooltip } from "@/lib/format";
import type { Span } from "@/lib/api-types";

// One attribute value cell: wraps long values and reveals a copy affordance on
// hover. `name` is the attribute key, used for the accessible label.
function AttrValue({ name, value }: { name: string; value: string }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // clipboard blocked (insecure context) — no-op
    }
  };
  return (
    <dd className="group flex min-w-0 items-start gap-1 font-mono text-xs">
      <span className="min-w-0 break-all">{value}</span>
      <button
        aria-label={`Copy ${name}`}
        onClick={copy}
        className="invisible mt-0.5 shrink-0 text-base-content/40 hover:text-base-content group-hover:visible"
      >
        {copied ? <Check className="h-3 w-3" /> : <Copy className="h-3 w-3" />}
      </button>
    </dd>
  );
}

function AttrTable({ title, attrs }: { title: string; attrs?: Record<string, string> }) {
  const entries = Object.entries(attrs ?? {});
  if (!entries.length) return null;
  return (
    <div>
      <h4 className="mb-1 text-[10px] font-semibold uppercase tracking-wider text-base-content/50">
        {title}
      </h4>
      {/* Key column is capped at 45% so long keys can never crush the values. */}
      <dl className="grid grid-cols-[minmax(120px,45%)_1fr] gap-x-4 gap-y-0.5">
        {entries.map(([k, v]) => (
          <div key={k} className="contents">
            <dt className="break-all font-mono text-xs text-base-content/55">{k}</dt>
            <AttrValue name={k} value={v} />
          </div>
        ))}
      </dl>
    </div>
  );
}
```

Key changes vs current: grid `minmax(140px,auto)` → `minmax(120px,45%)`; `dt` loses `truncate`, gains `break-all`; values move into `AttrValue` with the same Copy→Check flash pattern as `views/trace-json.tsx`.

- [x] **Step 2: Reduce the body padding**

In `SpanDetail`'s root div, change `px-9` to `px-4`.

- [x] **Step 3: Lint + typecheck**

Run: `cd ui && npm run lint && npm run typecheck`
Expected: clean.

- [x] **Step 4: Run the two now-covered tests — expect green**

Run: `cd ui && AVURUOPS_BASE_URL=http://localhost:3000 npx playwright test -g "stay readable|copies to clipboard"`
Expected: both PASS (dev server picks the change up on reload).

- [x] **Step 5: Commit**

```bash
git add ui/src/components/traces/span-detail.tsx
git commit -m "fix(ui): span attribute values no longer crushed by long keys; add copy"
```

---

### Task 3: useLocalStorageNumber hook + resizable panel

**Files:**
- Create: `ui/src/hooks/use-local-storage-number.ts`
- Modify: `ui/src/components/traces/trace-detail-panel.tsx`

- [x] **Step 1: Create the hook (mirrors `use-local-storage-flag.ts`)**

```tsx
"use client";

import { useCallback, useSyncExternalStore } from "react";

const CHANGE_EVENT = "avuru-storage-number";

function subscribe(callback: () => void) {
  window.addEventListener("storage", callback);
  window.addEventListener(CHANGE_EVENT, callback);
  return () => {
    window.removeEventListener("storage", callback);
    window.removeEventListener(CHANGE_EVENT, callback);
  };
}

// Numeric value persisted in localStorage, hydration-safe via
// useSyncExternalStore (server snapshot = fallback, client re-reads on mount).
export function useLocalStorageNumber(
  key: string,
  fallback: number,
): [number, (v: number) => void] {
  const value = useSyncExternalStore(
    subscribe,
    () => {
      const parsed = Number(localStorage.getItem(key));
      return Number.isFinite(parsed) && localStorage.getItem(key) !== null
        ? parsed
        : fallback;
    },
    () => fallback,
  );
  const setValue = useCallback(
    (v: number) => {
      localStorage.setItem(key, String(v));
      window.dispatchEvent(new Event(CHANGE_EVENT));
    },
    [key],
  );
  return [value, setValue];
}
```

- [x] **Step 2: Add the resize handle and width state to `trace-detail-panel.tsx`**

New imports (`useState` comes later in Task 4 — adding it now would fail lint as unused):

```tsx
import { useMemo, useRef } from "react";
import { useLocalStorageNumber } from "@/hooks/use-local-storage-number";
```

Module-level constants and handle component (above `TraceDetailPanel`):

```tsx
const SPAN_PANEL_WIDTH_KEY = "avuru-span-detail-width";
const SPAN_PANEL_DEFAULT = 384; // matches the old w-96
const SPAN_PANEL_MIN = 320;

// Draggable divider for the span-detail aside. Pointer capture keeps the drag
// alive when the cursor leaves the 6px hit area.
function ResizeHandle({
  onDrag,
  onNudge,
  onReset,
}: {
  onDrag: (clientX: number) => void;
  onNudge: (deltaPx: number) => void;
  onReset: () => void;
}) {
  const dragging = useRef(false);
  return (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-label="Resize span detail"
      tabIndex={0}
      onPointerDown={(e) => {
        e.preventDefault();
        dragging.current = true;
        e.currentTarget.setPointerCapture(e.pointerId);
      }}
      onPointerMove={(e) => dragging.current && onDrag(e.clientX)}
      onPointerUp={(e) => {
        dragging.current = false;
        e.currentTarget.releasePointerCapture(e.pointerId);
      }}
      onDoubleClick={onReset}
      onKeyDown={(e) => {
        if (e.key === "ArrowLeft") {
          e.preventDefault();
          onNudge(16);
        }
        if (e.key === "ArrowRight") {
          e.preventDefault();
          onNudge(-16);
        }
      }}
      className="w-1.5 shrink-0 cursor-col-resize touch-none bg-transparent transition-colors hover:bg-primary/40 focus-visible:bg-primary/40"
    />
  );
}
```

Inside `TraceDetailPanel`, add state next to the existing hooks:

```tsx
const bodyRef = useRef<HTMLDivElement>(null);
const [panelWidth, setPanelWidth] = useLocalStorageNumber(
  SPAN_PANEL_WIDTH_KEY,
  SPAN_PANEL_DEFAULT,
);

// Clamp to [320px, 70% of the workspace body], measured live during drag.
const clampWidth = (w: number) => {
  const body = bodyRef.current;
  const max = body
    ? Math.max(SPAN_PANEL_MIN, Math.round(body.getBoundingClientRect().width * 0.7))
    : SPAN_PANEL_DEFAULT;
  return Math.min(Math.max(Math.round(w), SPAN_PANEL_MIN), max);
};
```

Attach the ref to the body row: `<div ref={bodyRef} className="flex min-h-0 flex-1">`.

Replace the aside block (`{!comparing && selectedSpan && (...)}`) with:

```tsx
{!comparing && selectedSpan && (
  <>
    <ResizeHandle
      onDrag={(clientX) => {
        const body = bodyRef.current;
        if (body) setPanelWidth(clampWidth(body.getBoundingClientRect().right - clientX));
      }}
      onNudge={(d) => setPanelWidth(clampWidth(panelWidth + d))}
      onReset={() => setPanelWidth(SPAN_PANEL_DEFAULT)}
    />
    <aside
      style={{ width: panelWidth }}
      className="flex min-w-80 max-w-[70%] shrink-0 flex-col overflow-auto border-l border-neutral bg-base-100"
    >
      <div className="flex items-center justify-between border-b border-neutral px-3 py-2">
        <span className="text-xs font-semibold">Span detail</span>
        <button
          aria-label="Close span detail"
          onClick={() => setMany({ span: undefined })}
          className="text-base-content/50 hover:text-base-content"
        >
          <X className="h-4 w-4" />
        </button>
      </div>
      <SpanDetail span={selectedSpan} />
    </aside>
  </>
)}
```

Notes: `w-96` is gone; `min-w-80 max-w-[70%]` also clamps stale stored values at render time (the JS clamp only runs on drag/nudge).

- [x] **Step 3: Lint + typecheck**

Run: `cd ui && npm run lint && npm run typecheck`
Expected: clean.

- [x] **Step 4: Run the resize test — expect green**

Run: `cd ui && AVURUOPS_BASE_URL=http://localhost:3000 npx playwright test -g "resizes by drag"`
Expected: PASS.

- [x] **Step 5: Commit**

```bash
git add ui/src/hooks/use-local-storage-number.ts ui/src/components/traces/trace-detail-panel.tsx
git commit -m "feat(ui): span detail panel is drag-resizable with persisted width"
```

---

### Task 4: Expand overlay

**Files:**
- Create: `ui/src/components/traces/span-detail-overlay.tsx`
- Modify: `ui/src/components/traces/trace-detail-panel.tsx`

- [x] **Step 1: Create the overlay component**

```tsx
"use client";

import { useEffect } from "react";
import { createPortal } from "react-dom";
import { X } from "lucide-react";
import type { Span } from "@/lib/api-types";
import { SpanDetail } from "./span-detail";

// Full-size zoom of the span detail, portalled above the fullscreen layer
// (z-50). Esc is handled in the CAPTURE phase with stopImmediatePropagation so
// it closes only the overlay — trace-workspace also listens for Esc on
// document to exit fullscreen, and must not fire while the overlay is open.
export function SpanDetailOverlay({ span, onClose }: { span: Span; onClose: () => void }) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        e.stopImmediatePropagation();
        onClose();
      }
    };
    document.addEventListener("keydown", onKey, true);
    return () => document.removeEventListener("keydown", onKey, true);
  }, [onClose]);

  if (typeof document === "undefined") return null;
  return createPortal(
    <div
      className="fixed inset-0 z-60 flex items-center justify-center bg-black/50 p-4"
      onClick={onClose}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-label="Span detail expanded"
        onClick={(e) => e.stopPropagation()}
        className="flex max-h-[85vh] w-[min(1100px,92vw)] flex-col overflow-hidden rounded-xl border border-neutral bg-base-100 shadow-xl"
      >
        <div className="flex items-center justify-between border-b border-neutral px-4 py-2">
          <span className="truncate font-mono text-xs font-semibold">{span.name}</span>
          <button
            aria-label="Close expanded span detail"
            onClick={onClose}
            className="text-base-content/50 hover:text-base-content"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
        <div className="min-h-0 overflow-auto">
          <SpanDetail span={span} />
        </div>
      </div>
    </div>,
    document.body,
  );
}
```

Check `Span` in `ui/src/lib/api-types.ts` for the operation-name field — if it is not `name` (e.g. `operationName`), use that field in the header instead.

- [x] **Step 2: Wire the expand button + overlay into `trace-detail-panel.tsx`**

Add imports `import { SpanDetailOverlay } from "./span-detail-overlay";` and `useState` (react import becomes `import { useMemo, useRef, useState } from "react";`), plus state `const [expanded, setExpanded] = useState(false);`.

In the aside header, wrap the close button so expand sits beside it:

```tsx
<div className="flex items-center gap-1.5">
  <button
    aria-label="Expand span detail"
    onClick={() => setExpanded(true)}
    className="text-base-content/50 hover:text-base-content"
  >
    <Maximize2 className="h-4 w-4" />
  </button>
  <button
    aria-label="Close span detail"
    onClick={() => setMany({ span: undefined })}
    className="text-base-content/50 hover:text-base-content"
  >
    <X className="h-4 w-4" />
  </button>
</div>
```

(`Maximize2` is already imported in this file.)

After the `</aside>` closing tag, still inside the fragment:

```tsx
{expanded && (
  <SpanDetailOverlay span={selectedSpan} onClose={() => setExpanded(false)} />
)}
```

- [x] **Step 3: Lint + typecheck**

Run: `cd ui && npm run lint && npm run typecheck`
Expected: clean.

- [x] **Step 4: Run the overlay test — expect green**

Run: `cd ui && AVURUOPS_BASE_URL=http://localhost:3000 npx playwright test -g "expand overlay"`
Expected: PASS, including the Esc-does-not-exit-fullscreen assertions.

- [x] **Step 5: Commit**

```bash
git add ui/src/components/traces/span-detail-overlay.tsx ui/src/components/traces/trace-detail-panel.tsx
git commit -m "feat(ui): expand span detail into a wide overlay"
```

---

### Task 5: Full verification

- [x] **Step 1: Run the entire e2e suite the official way**

Run from repo root: `make e2e-ui`
Expected: all tests pass (this rebuilds the UI image with the changes, reseeds from the updated fixture, runs the whole suite against :3001, and tears down). This is the success gate — do not claim completion without it.

- [x] **Step 2: Fix anything that fails, re-run, then stop the dev server**

If the dev server from Task 1 is still running, kill it. Working tree should be clean (`git status`).
