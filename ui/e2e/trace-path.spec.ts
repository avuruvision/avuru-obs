import { test, expect } from "@playwright/test";

// Seeded fixtures (deploy/compose/seed/fixtures):
//  - the multiservice trace: seed-gateway calls seed-payments.
//  - the checkout trace: seed-checkout, whose leaf client spans call PostgreSQL
//    and Redis — dependencies that never sent a span of their own.
const MULTI_TRACE_ID = "eeee5555ffff6666aaaa7777bbbb8888";
const SEED_TRACE_ID = "aaaa1111bbbb2222cccc3333dddd4444";
const ROOT_SERVICE = "seed-gateway";
const CHILD_SERVICE = "seed-payments";

const pathUrl = (traceId: string) =>
  `/traces?tab=traces&trace=${traceId}&view=path`;

// Every lookup is scoped to the canvas: the summary bar above it carries a
// service legend with the same names, so an unscoped text match is ambiguous
// by construction.
const canvas = (page: import("@playwright/test").Page) =>
  page.getByTestId("trace-path");

test.describe("trace path", () => {
  test("draws the services one request crossed, in order", async ({ page }) => {
    await page.goto(pathUrl(MULTI_TRACE_ID));

    await expect(canvas(page).getByText(ROOT_SERVICE, { exact: true })).toBeVisible();
    await expect(canvas(page).getByText(CHILD_SERVICE, { exact: true })).toBeVisible();
    // Where the request entered is stated, not left to be inferred from
    // left-most position.
    await expect(canvas(page).getByText("entry", { exact: true })).toBeVisible();
  });

  test("shows dependencies that never reported a span", async ({ page }) => {
    await page.goto(pathUrl(SEED_TRACE_ID));

    // The database and cache exist only as the caller's exit spans. Leaving
    // them out would end the path at the service that called them and hide the
    // hop the time often went into.
    await expect(canvas(page).getByText(/no telemetry/i).first()).toBeVisible();
    await expect(canvas(page).getByText("at caller").first()).toBeVisible();
  });

  test("focusing a service shows only what it caused", async ({ page }) => {
    await page.goto(pathUrl(MULTI_TRACE_ID));
    await expect(canvas(page).getByText(ROOT_SERVICE, { exact: true })).toBeVisible();

    await canvas(page).getByRole("button", { name: `Focus on ${CHILD_SERVICE}` }).click();
    await expect(canvas(page).getByText(`Showing what ${CHILD_SERVICE} caused.`)).toBeVisible();
    // Its caller is upstream of the focus, so it leaves the graph.
    await expect(canvas(page).getByText(ROOT_SERVICE, { exact: true })).toHaveCount(0);

    // exact: the per-card button is "Clear focus on <service>".
    await canvas(page).getByRole("button", { name: "Clear focus", exact: true }).click();
    await expect(canvas(page).getByText(ROOT_SERVICE, { exact: true })).toBeVisible();
  });

  test("a service card selects one of its spans", async ({ page }) => {
    await page.goto(pathUrl(MULTI_TRACE_ID));
    await canvas(page).getByText(CHILD_SERVICE, { exact: true }).click();
    await expect(page.getByText("Span detail", { exact: true })).toBeVisible();
  });
  // The numbers on each card come from the HUB now, not from arithmetic in the
  // browser. Before this the spec asserted only the shape of the graph, so the
  // client-side rollup could have been deleted without anything noticing.
  //
  // The seeded multiservice trace is deterministic: seed-gateway keeps 100ms
  // across 2 spans, and seed-payments 200ms across 1 — which is exactly what
  // GET /api/v1/traces/{id} reports in its `services` rollup.
  test("each service reports the time the hub says it spent", async ({ page }) => {
    await page.goto(pathUrl(MULTI_TRACE_ID));

    // Card text renders without separators ("100msself·2spansfocus"), so match
    // the values rather than word-spaced phrases.
    const root = canvas(page).getByTestId(`path-node-${ROOT_SERVICE}`);
    await expect(root).toBeVisible();
    await expect(root).toContainText("100ms");
    await expect(root).toContainText("2");

    const child = canvas(page).getByTestId(`path-node-${CHILD_SERVICE}`);
    await expect(child).toContainText("200ms");

    // Exact values rather than "non-zero": a broken hand-off renders 0ms and
    // 0 spans, which these assertions already exclude — and a negative check
    // for "0ms" would match "100ms" anyway.
  });
});
