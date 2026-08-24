import { test, expect } from "@playwright/test";

// Undetected peers and the always-on edge volume
// (design/2026-08-24-map-encoding.md).
//
// The hub reports every edge it observed, including ones whose far end never
// sent a span — an eBPF flow to a workload with no telemetry of its own. The
// renderer used to DROP those edges, because a graph edge needs two nodes and
// only one existed, so the one screen built to show connections was deleting
// the connections it could least afford to lose.
//
// Stubbed rather than seeded: reproducing an unresolved endpoint needs a real
// eBPF sensor, and the contract under test is entirely in the view. Same
// technique as service-map-mesh.spec.ts.
const MAP_WITH_PEER = {
  services: [
    { name: "checkout", spanCount: 40, ratePerSec: 1, errorRate: 0, p50Ms: 5, p95Ms: 9, p99Ms: 12 },
    {
      name: "waypoint.istio-waypoint",
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
    // A traced call to something that never reported: the peer.
    { source: "checkout", target: "legacy-billing", calls: 120, errorCount: 0, errorRate: 0, provenance: "trace", p50Ms: 8, p95Ms: 20 },
    // A kernel-observed connection with no traced call behind it — bytes, not
    // calls, which is why its label must not read as a rate.
    { source: "checkout", target: "unknown-sink", calls: 0, errorCount: 0, errorRate: 0, bytes: 40960, provenance: "flow" },
    // The mesh hop. Hidden by default, and it must NOT come back as a peer.
    { source: "checkout", target: "waypoint.istio-waypoint", calls: 12, errorCount: 0, errorRate: 0, provenance: "trace", p50Ms: 5, p95Ms: 9 },
  ],
};

test.describe("service map peers", () => {
  test.beforeEach(async ({ page }) => {
    await page.route("**/api/v1/service-map*", (route) => route.fulfill({ json: MAP_WITH_PEER }));
  });

  test("recovers the far end of a connection nobody instrumented", async ({ page }) => {
    await page.goto("/service-map");

    // One application, two peers. They are counted apart because they are not
    // services: nothing was deployed under those names as far as we know.
    const count = page.getByTestId("map-count");
    await expect(count).toContainText("1 services");
    await expect(count).toContainText("2 undetected peers");

    await expect(page.getByTestId("map-legend")).toContainText(
      "hollow outline = seen in traffic, never heard from",
    );
  });

  test("a hidden mesh proxy does not come back as a peer", async ({ page }) => {
    await page.goto("/service-map");

    // The regression this ordering exists to prevent: hiding the mesh drops the
    // proxy AND its edges, so synthesizing peers afterwards finds nothing to
    // resurrect. Running it first would redraw the waypoint as an "undetected
    // peer" — the same false dependency v0.7 removed, wearing a new shape.
    await expect(page.getByTestId("map-count")).toContainText("2 undetected peers");
    await expect(page.getByTestId("map-count")).toContainText("1 mesh/gateway node hidden");

    // With the mesh shown the proxy is a known node again, so the count of
    // peers does not move.
    await page.getByRole("checkbox", { name: "Show mesh & gateways" }).check();
    await expect(page.getByTestId("map-count")).toContainText("2 services");
    await expect(page.getByTestId("map-count")).toContainText("2 undetected peers");
  });

  test("edge volume is a choice, held in the URL", async ({ page }) => {
    await page.goto("/service-map");

    const toggle = page.getByRole("checkbox", { name: "Edge volume" });
    // Off by default: on a dense graph every label is a label too many.
    await expect(toggle).not.toBeChecked();
    await expect(page).not.toHaveURL(/edgeLabels=/);

    await toggle.check();
    await expect(page).toHaveURL(/edgeLabels=true/);

    // The labels are drawn to a canvas, so what the DOM can hold is the choice
    // surviving a reload — the map itself is verified by eye.
    await page.reload();
    await expect(page.getByRole("checkbox", { name: "Edge volume" })).toBeChecked();
  });
});
