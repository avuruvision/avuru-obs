import { test, expect } from "@playwright/test";

// The mesh-hop defect and its fix.
//
// A service-mesh proxy emits spans and exchanges bytes exactly like an
// application, so `app -> waypoint -> app` arrives as two ordinary call edges
// and the map asserts a dependency between two services that never call each
// other. The hub now labels those workloads role="transport" and the map drops
// them by default.
//
// The API is stubbed rather than seeded: reproducing this needs a mesh in the
// stack, and the contract under test is entirely in the view — what gets
// hidden, what the count line claims, and whether the toggle round-trips
// through the URL. Same technique as the module-off test in service-map.spec.
const MESH_MAP = {
  services: [
    { name: "auth-service", spanCount: 40, ratePerSec: 1, errorRate: 0, p50Ms: 5, p95Ms: 9, p99Ms: 12 },
    { name: "checkout", spanCount: 30, ratePerSec: 1, errorRate: 0, p50Ms: 4, p95Ms: 8, p99Ms: 11 },
    {
      name: "global-waypoint.istio-waypoint",
      spanCount: 70,
      ratePerSec: 2,
      errorRate: 0,
      p50Ms: 1,
      p95Ms: 2,
      p99Ms: 3,
      role: "transport",
    },
  ],
  edges: [
    // The two halves of one hop. Neither is a dependency.
    { source: "auth-service", target: "global-waypoint.istio-waypoint", calls: 12, errorCount: 0, errorRate: 0, provenance: "trace", p50Ms: 5, p95Ms: 9 },
    { source: "global-waypoint.istio-waypoint", target: "checkout", calls: 12, errorCount: 0, errorRate: 0, provenance: "trace", p50Ms: 4, p95Ms: 8 },
    // A connection the sensor saw with no traced call behind it: real, and not
    // a call, which is why the count line must not add it to "call edges".
    { source: "auth-service", target: "checkout", calls: 0, errorCount: 0, errorRate: 0, bytes: 40960, provenance: "flow" },
  ],
};

// The same cluster once the hub walks the traces across the proxy: the two hops
// are still reported, and so is the dependency underneath them. Drawing all
// three at once would count the same twelve calls twice, which is the rule the
// second test here exists to hold.
const COLLAPSED_MAP = {
  services: MESH_MAP.services,
  edges: [
    // The hops, unchanged — the hub still reports them.
    MESH_MAP.edges[0],
    MESH_MAP.edges[1],
    // The pair's own edge, now carrying the twelve calls the walk recovered on
    // top of the bytes the kernel already saw. This is what the hub's merge
    // produces: one edge per pair, with the mesh-carried portion marked.
    {
      source: "auth-service",
      target: "checkout",
      calls: 12,
      errorCount: 1,
      errorRate: 1 / 12,
      bytes: 40960,
      provenance: "flow",
      p50Ms: 6,
      p95Ms: 11,
      viaTransport: ["global-waypoint.istio-waypoint"],
      collapsedCalls: 12,
      collapsedErrorCount: 1,
    },
  ],
};

test.describe("service map with a mesh", () => {
  test.beforeEach(async ({ page }) => {
    await page.route("**/api/v1/service-map*", (route) => route.fulfill({ json: MESH_MAP }));
  });

  test("hides mesh hops by default and counts flows apart from calls", async ({ page }) => {
    await page.goto("/service-map");

    const count = page.getByTestId("map-count");
    // Both trace edges touched the waypoint, so both go with it: what is left
    // is the two applications and the one observed connection between them.
    await expect(count).toContainText("2 services · 0 call edges");
    await expect(count).toContainText("1 network flow");
    await expect(count).toContainText("1 mesh/gateway node hidden");
  });

  test("the toggle brings the mesh back and round-trips through the URL", async ({ page }) => {
    await page.goto("/service-map");

    await page.getByRole("checkbox", { name: "Show mesh & gateways" }).check();
    await expect(page).toHaveURL(/infra=true/);

    const count = page.getByTestId("map-count");
    await expect(count).toContainText("3 services · 2 call edges");
    await expect(count).not.toContainText("hidden");
    await expect(page.getByTestId("map-legend")).toContainText("diamond = mesh or gateway");

    // The URL is the truth, not component state.
    await page.reload();
    await expect(page.getByTestId("map-count")).toContainText("3 services · 2 call edges");
  });

  test("offers no mesh toggle on a cluster without one", async ({ page }) => {
    await page.route("**/api/v1/service-map*", (route) =>
      route.fulfill({ json: { services: MESH_MAP.services.slice(0, 2), edges: [] } }),
    );
    await page.goto("/service-map");

    await expect(page.getByTestId("map-count")).toContainText("2 services · 0 call edges");
    await expect(page.getByRole("checkbox", { name: "Show mesh & gateways" })).toHaveCount(0);
  });

  test("draws the dependency recovered across the hop, and names the proxy", async ({ page }) => {
    await page.route("**/api/v1/service-map*", (route) => route.fulfill({ json: COLLAPSED_MAP }));
    await page.goto("/service-map");

    // Before this feature a meshed cluster showed two services and NO edges —
    // honest about the hops, silent about the dependency they carried.
    const count = page.getByTestId("map-count");
    await expect(count).toContainText("2 services · 1 call edge");
    await expect(count).toContainText("1 through the mesh");
    await expect(page.getByTestId("map-legend")).toContainText("recovered across a mesh hop");
  });

  test("swaps the recovered edge for the hops rather than drawing both", async ({ page }) => {
    await page.route("**/api/v1/service-map*", (route) => route.fulfill({ json: COLLAPSED_MAP }));
    await page.goto("/service-map?infra=true");

    // The twelve calls are the SAME twelve, whichever way they are drawn: two
    // hops here, one dependency without the toggle — never three edges.
    const count = page.getByTestId("map-count");
    await expect(count).toContainText("3 services · 2 call edges");
    await expect(count).not.toContainText("through the mesh");
    // The kernel's own observation of that pair is not a span and does not go
    // with the calls: it was true before the mesh existed and stays true here.
    await expect(count).toContainText("1 network flow");
  });
});
