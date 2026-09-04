import { test, expect } from "@playwright/test";

// The neighbourhood diagram's honesty rules.
//
// The API is stubbed rather than seeded: a mesh-recovered hop, a derived
// database and an eBPF-only peer cannot all be produced by one seeded stack,
// and the contract under test is entirely in the view — what the picture
// claims about edges nobody measured directly. Same technique as
// service-map-mesh.spec.ts.
//
// `range=15m` is pinned because the diagram turns call counts into a rate
// against the window, and the window is otherwise remembered per browser.
const FOCUS = "orders-api";

const MAP = {
  services: [
    { name: FOCUS, spanCount: 90, ratePerSec: 1, errorRate: 0, p50Ms: 4, p95Ms: 9, p99Ms: 14 },
    { name: "api-gateway", spanCount: 60, ratePerSec: 1, errorRate: 0, p50Ms: 3, p95Ms: 7, p99Ms: 10 },
    { name: "ledger", spanCount: 30, ratePerSec: 0.5, errorRate: 0.1, p50Ms: 20, p95Ms: 40, p99Ms: 60 },
    {
      name: "postgresql://orders-db",
      spanCount: 0,
      ratePerSec: 0,
      errorRate: 0,
      p50Ms: 3,
      p95Ms: 5,
      p99Ms: 8,
      role: "virtual",
      kind: "database",
    },
  ],
  edges: [
    // A dependency the hub could only see by walking the trace across a proxy.
    {
      source: "api-gateway",
      target: FOCUS,
      calls: 60,
      errorCount: 0,
      errorRate: 0,
      provenance: "trace",
      p50Ms: 6,
      p95Ms: 12,
      viaTransport: ["istio-proxy"],
      collapsedCalls: 60,
    },
    { source: FOCUS, target: "ledger", calls: 30, errorCount: 3, errorRate: 0.1, provenance: "trace", p50Ms: 20, p95Ms: 40 },
    { source: FOCUS, target: "postgresql://orders-db", calls: 12, errorCount: 0, errorRate: 0, provenance: "trace", p50Ms: 3, p95Ms: 5 },
    // A connection the kernel saw and nobody traced: bytes, no calls, no p95.
    { source: FOCUS, target: "cache-peer", calls: 0, errorCount: 0, errorRate: 0, bytes: 40960, provenance: "flow" },
  ],
};

// The same neighbourhood on a cluster where the proxy is itself reported — the
// shape a real meshed install sends, and the one the stub above leaves out.
// The hub reports the two hops AND the dependency it recovered from them: the
// sixty calls are the same sixty, described twice.
const MESHED = {
  services: [
    ...MAP.services,
    {
      name: "istio-proxy",
      spanCount: 120,
      ratePerSec: 2,
      errorRate: 0,
      p50Ms: 1,
      p95Ms: 2,
      p99Ms: 3,
      role: "transport",
    },
  ],
  edges: [
    ...MAP.edges,
    { source: "api-gateway", target: "istio-proxy", calls: 60, errorCount: 0, errorRate: 0, provenance: "trace", p50Ms: 5, p95Ms: 10 },
    { source: "istio-proxy", target: FOCUS, calls: 60, errorCount: 0, errorRate: 0, provenance: "trace", p50Ms: 4, p95Ms: 8 },
  ],
};

test.describe("service neighbourhood diagram", () => {
  test.beforeEach(async ({ page }) => {
    await page.route("**/api/v1/service-map*", (route) => route.fulfill({ json: MAP }));
  });

  test("says when a dependency was recovered across a proxy", async ({ page }) => {
    await page.goto(`/services?service=${FOCUS}&range=15m`);

    const diagram = page.getByTestId("service-neighbourhood");
    await expect(diagram).toBeVisible();
    // A reconstructed edge must never read as a directly observed one.
    await expect(diagram).toContainText("via istio-proxy");
  });

  test("draws what sends no telemetry as an outline, and refuses to open it", async ({ page }) => {
    await page.goto(`/services?service=${FOCUS}&range=15m`);
    const diagram = page.getByTestId("service-neighbourhood");

    // A derived dependency: the scheme is dropped from the label the way the
    // map drops it, and the card says where its numbers actually came from.
    await expect(diagram).toContainText("orders-db");
    await expect(diagram).toContainText("database · measured at the caller");
    // It has no page of its own to open, so it is not a button at all.
    await expect(diagram.getByRole("button", { name: /orders-db/ })).toHaveCount(0);

    // An endpoint that never sent a span still gets a card — deleting the edge
    // would hide the part of the estate nobody has instrumented yet — but it
    // carries no numbers, because there are none.
    await expect(diagram).toContainText("cache-peer");
    await expect(diagram).toContainText("no telemetry of its own");
    await expect(diagram.getByRole("button", { name: /cache-peer/ })).toHaveCount(0);
  });

  test("a connection with no traced call shows bytes, not a rate of zero", async ({ page }) => {
    await page.goto(`/services?service=${FOCUS}&range=15m`);
    const diagram = page.getByTestId("service-neighbourhood");

    await expect(diagram).toContainText("40 KB");
    // "0.0/min" would read as "this path is idle"; the truth is that nothing
    // measured it. Same reason the edge carries no p95.
    await expect(diagram).not.toContainText("0.0/min");
    await expect(diagram).not.toContainText("0µs");
  });

  test("stops drawing past a readable number of peers and says how many are left", async ({ page }) => {
    const callers = Array.from({ length: 10 }, (_, i) => `caller-${i}`);
    await page.route("**/api/v1/service-map*", (route) =>
      route.fulfill({
        json: {
          services: [
            MAP.services[0],
            ...callers.map((name) => ({
              name,
              spanCount: 10,
              ratePerSec: 0.1,
              errorRate: 0,
              p50Ms: 1,
              p95Ms: 2,
              p99Ms: 3,
            })),
          ],
          edges: callers.map((name, i) => ({
            source: name,
            target: FOCUS,
            calls: 100 - i,
            errorCount: 0,
            errorRate: 0,
            provenance: "trace",
            p50Ms: 1,
            p95Ms: 2,
          })),
        },
      }),
    );
    await page.goto(`/services?service=${FOCUS}&range=15m`);

    const diagram = page.getByTestId("service-neighbourhood");
    // Eight drawn, most traffic first, and the rest named rather than dropped.
    await expect(diagram).toContainText("+2 more");
    await expect(diagram).toContainText("caller-0");
    await expect(diagram).not.toContainText("caller-9");

    // The table stays the complete list, which is what makes truncating honest.
    await page.getByRole("button", { name: "Table" }).click();
    await expect(page.getByRole("row").filter({ hasText: "caller-9" })).toBeVisible();
  });

  test("an entry point says it has no callers rather than showing an empty side", async ({ page }) => {
    await page.goto(`/services?service=api-gateway&range=15m`);

    const diagram = page.getByTestId("service-neighbourhood");
    await expect(diagram).toContainText("No callers observed");
    await expect(diagram).toContainText(FOCUS);
  });
  test("counts a meshed caller once, not twice", async ({ page }) => {
    await page.route("**/api/v1/service-map*", (route) => route.fulfill({ json: MESHED }));
    await page.goto(`/services?service=${FOCUS}&range=15m`);

    const diagram = page.getByTestId("service-neighbourhood");
    await expect(diagram).toBeVisible();

    // The regression: the page took the map's raw edge set, so it drew the
    // recovered dependency AND the hop that produced it — one real caller
    // shown as two, with the traffic counted twice. The proxy is transport,
    // not a caller.
    await expect(diagram).not.toContainText("istio-proxy card");
    await expect(diagram.getByRole("button", { name: /^istio-proxy/ })).toHaveCount(0);
    await expect(diagram).toContainText("api-gateway");
    // Still labelled, because the dependency behind the proxy is exactly what
    // is being drawn.
    await expect(diagram).toContainText("via istio-proxy");

    // One caller in, three dependencies out — the proxy is in neither count.
    await expect(page.getByText("1 in · 3 out")).toBeVisible();
  });

  test("keeps the proxy when the page is about the proxy", async ({ page }) => {
    await page.route("**/api/v1/service-map*", (route) => route.fulfill({ json: MESHED }));
    await page.goto(`/services?service=istio-proxy&range=15m`);

    // Hiding transport must not delete the subject of the page: a proxy has a
    // neighbourhood too, and it is the one screen where its hops ARE the point.
    const diagram = page.getByTestId("service-neighbourhood");
    await expect(diagram).toBeVisible();
    await expect(diagram).toContainText("api-gateway");
    await expect(diagram).toContainText(FOCUS);
    await expect(page.getByText("1 in · 1 out")).toBeVisible();
  });
});
