import { test, expect } from "@playwright/test";

// The mesh screen, stubbed at the API. Reproducing this against the compose
// stack would need a real service mesh in it; the contract under test is what
// the screen SAYS — which proxies it lists, and what it claims about a control
// plane nobody is watching.
const PROXIES = {
  proxies: [
    {
      name: "istio-ingressgateway-istio.istio-edge",
      namespace: "istio-edge",
      role: "ingress-gateway",
      ratePerSec: 12,
      errorRate: 0.02,
      p50Ms: 3,
      p95Ms: 9,
      callsIn: 700,
      callsOut: 690,
    },
    {
      // Traffic arriving, nothing forwarded: a proxy that has stopped doing the
      // one thing it exists to do, with a success rate that looks fine.
      name: "global-waypoint.istio-waypoint",
      namespace: "istio-waypoint",
      role: "waypoint",
      ratePerSec: 4,
      errorRate: 0,
      p50Ms: 1,
      p95Ms: 2,
      callsIn: 240,
      callsOut: 0,
    },
  ],
};

test.describe("mesh screen", () => {
  test("lists the proxies every other screen hides", async ({ page }) => {
    await page.route("**/api/v1/mesh/proxies*", (r) => r.fulfill({ json: PROXIES }));
    await page.route("**/api/v1/mesh/control-plane*", (r) =>
      r.fulfill({
        json: {
          available: true,
          lastSeen: new Date().toISOString(),
          connectedProxies: 12,
          pushes: 400,
          rejectedConfigs: 3,
          convergenceP95Ms: 250,
        },
      }),
    );
    await page.goto("/mesh");

    const table = page.getByTestId("mesh-proxies");
    await expect(table).toContainText("istio-ingressgateway-istio.istio-edge");
    await expect(table).toContainText("global-waypoint.istio-waypoint");

    // Rejected configuration is the signal nothing else in the product carries.
    // The healthy card names which control plane the numbers describe.
    await expect(page.getByTestId("mesh-control-plane")).toContainText("Rejected configs");
    await expect(page.getByTestId("mesh-control-plane")).toContainText("3");
  });

  // Three silences, three fixes. They used to render one sentence, which sent
  // an operator to check a scrape that was working perfectly.
  for (const { state, heading, reason } of [
    {
      state: "unreachable",
      heading: "Control plane not answering",
      reason: "check mesh.controlPlane.endpoint",
    },
    {
      state: "unrecognised",
      heading: "Control plane not recognised",
      reason: "The control-plane view is Istio-shaped",
    },
  ]) {
    test(`names the fix for a ${state} control plane`, async ({ page }) => {
      await page.route("**/api/v1/mesh/proxies*", (r) => r.fulfill({ json: PROXIES }));
      await page.route("**/api/v1/mesh/control-plane*", (r) =>
        r.fulfill({ json: { available: false, state, reason } }),
      );
      await page.goto("/mesh");

      const card = page.getByTestId("mesh-control-plane");
      await expect(card).toContainText(heading);
      await expect(card).toContainText(reason);
      // Never a grid of zeros: that is the reassuring lie this card exists to
      // prevent.
      await expect(card).not.toContainText("Rejected configs");
    });
  }

  // An unrecognised control plane does not mean an unusable screen: the proxy
  // table comes from your own traces.
  test("says the proxies are still measured when the control plane is not read", async ({
    page,
  }) => {
    await page.route("**/api/v1/mesh/proxies*", (r) => r.fulfill({ json: PROXIES }));
    await page.route("**/api/v1/mesh/control-plane*", (r) =>
      r.fulfill({ json: { available: false, state: "unrecognised", reason: "…" } }),
    );
    await page.goto("/mesh");

    await expect(page.getByTestId("mesh-proxies")).toContainText("global-waypoint.istio-waypoint");
    await expect(page.getByTestId("mesh-control-plane")).toContainText(
      "The proxies above are still measured",
    );
  });

  test("states that the control plane is unwatched instead of reporting zeros", async ({
    page,
  }) => {
    await page.route("**/api/v1/mesh/proxies*", (r) => r.fulfill({ json: PROXIES }));
    await page.route("**/api/v1/mesh/control-plane*", (r) =>
      r.fulfill({
        json: { available: false, reason: "no control-plane metrics in this window" },
      }),
    );
    await page.goto("/mesh");

    const card = page.getByTestId("mesh-control-plane");
    await expect(card).toContainText("Control plane not observed");
    await expect(card).toContainText("no control-plane metrics in this window");
    // The reassuring lie this shape exists to prevent.
    await expect(card).not.toContainText("Rejected configs");
  });

  test("filters the proxy list", async ({ page }) => {
    await page.route("**/api/v1/mesh/proxies*", (r) => r.fulfill({ json: PROXIES }));
    await page.route("**/api/v1/mesh/control-plane*", (r) =>
      r.fulfill({ json: { available: false, reason: "not scraped" } }),
    );
    await page.goto("/mesh");

    await page.getByRole("searchbox", { name: "Filter proxies" }).fill("waypoint");
    await expect(page.getByTestId("mesh-proxies")).not.toContainText("ingressgateway");
    await expect(page).toHaveURL(/q=waypoint/);
  });

  // A role and a namespace are what make a fleet of forty proxies readable, so
  // both must survive the trip to the screen and both must be filterable.
  test("facets by role and namespace, and keeps them in the URL", async ({ page }) => {
    await page.route("**/api/v1/mesh/proxies*", (r) => r.fulfill({ json: PROXIES }));
    await page.route("**/api/v1/mesh/control-plane*", (r) =>
      r.fulfill({ json: { available: false, state: "unconfigured" } }),
    );
    await page.goto("/mesh");

    const table = page.getByTestId("mesh-proxies");
    await expect(table).toContainText("Waypoint");
    await expect(table).toContainText("istio-waypoint");

    await page.getByRole("button", { name: "Filter by role" }).click();
    await page.getByRole("option", { name: "Waypoint" }).click();

    await expect(page).toHaveURL(/role=waypoint/);
    await expect(table).toContainText("global-waypoint.istio-waypoint");
    await expect(table).not.toContainText("istio-ingressgateway-istio.istio-edge");
  });

  // Sorting is the whole reason to prefer a table to the map here: one click
  // must put the worst proxy on top and say so to assistive tech.
  test("sorts by a column the reader picks", async ({ page }) => {
    await page.route("**/api/v1/mesh/proxies*", (r) => r.fulfill({ json: PROXIES }));
    await page.route("**/api/v1/mesh/control-plane*", (r) =>
      r.fulfill({ json: { available: false, state: "unconfigured" } }),
    );
    await page.goto("/mesh");

    const rows = page.getByTestId("mesh-proxies").locator("tbody tr");
    // Worst success first is the default, so the 2% error gateway leads.
    await expect(rows.first()).toContainText("istio-ingressgateway-istio.istio-edge");

    await page.getByRole("button", { name: "Calls out" }).click();
    await expect(rows.first()).toContainText("istio-ingressgateway-istio.istio-edge");
    await page.getByRole("button", { name: "Calls out" }).click();
    // Ascending: the waypoint forwarding nothing is now top.
    await expect(rows.first()).toContainText("global-waypoint.istio-waypoint");
  });

  // The columns said "Carried" while rendering call counts for two releases.
  test("names the call-count columns as counts", async ({ page }) => {
    await page.route("**/api/v1/mesh/proxies*", (r) => r.fulfill({ json: PROXIES }));
    await page.route("**/api/v1/mesh/control-plane*", (r) =>
      r.fulfill({ json: { available: false, state: "unconfigured" } }),
    );
    await page.goto("/mesh");

    const table = page.getByTestId("mesh-proxies");
    await expect(table).toContainText("Calls in");
    await expect(table).toContainText("Calls out");
    await expect(table).not.toContainText("Carried");
  });

  // Bytes come from the flow metrics. On an install that collects them the
  // columns appear; on one that does not they must be absent rather than a
  // row of dashes, which would read as a fleet moving nothing.
  test("shows wire bytes only where something measured them", async ({ page }) => {
    const MEASURED = {
      proxies: [
        {
          ...PROXIES.proxies[0],
          bytesIn: 4096,
          bytesOut: 2048,
          rttMs: 41,
          failedConnections: 3,
          retransmits: 5,
        },
      ],
    };
    await page.route("**/api/v1/mesh/control-plane*", (r) =>
      r.fulfill({ json: { available: false, state: "unconfigured" } }),
    );

    await page.route("**/api/v1/mesh/proxies*", (r) => r.fulfill({ json: MEASURED }));
    await page.goto("/mesh");
    const table = page.getByTestId("mesh-proxies");
    await expect(table).toContainText("Bytes in");
    await expect(table).toContainText("Link p95");
    await expect(table).toContainText("4.0 KB");

    // Same screen, an install without the flow metrics: the columns go away.
    await page.route("**/api/v1/mesh/proxies*", (r) => r.fulfill({ json: PROXIES }));
    await page.reload();
    await expect(table).toContainText("Calls in");
    await expect(table).not.toContainText("Bytes in");
    await expect(table).not.toContainText("Link p95");
  });

  // The table says which proxy is unhealthy and then stops. This is the step
  // that was missing: what the proxy carries, and who loses it when it fails.
  test("opens a proxy and names the dependencies it carries", async ({ page }) => {
    await page.route("**/api/v1/mesh/proxies*", (r) => r.fulfill({ json: PROXIES }));
    await page.route("**/api/v1/mesh/control-plane*", (r) =>
      r.fulfill({ json: { available: false, state: "unconfigured" } }),
    );
    await page.route("**/api/v1/service-map*", (r) =>
      r.fulfill({
        json: {
          services: [],
          edges: [
            {
              source: "checkout",
              target: "payments",
              calls: 210,
              errorCount: 0,
              errorRate: 0,
              p95Ms: 42,
              // Ambient's normal path: client ztunnel, waypoint, server ztunnel.
              viaTransport: ["ztunnel", "global-waypoint.istio-waypoint", "ztunnel"],
            },
            {
              // Carried by a different proxy — must not appear on this page.
              source: "search",
              target: "catalog",
              calls: 99,
              errorCount: 0,
              errorRate: 0,
              viaTransport: ["istio-ingressgateway-istio.istio-edge"],
            },
          ],
        },
      }),
    );
    await page.goto("/mesh");

    await page.getByRole("button", { name: "global-waypoint.istio-waypoint" }).click();
    await expect(page).toHaveURL(/proxy=global-waypoint/);

    const carried = page.getByTestId("mesh-carried");
    await expect(carried).toContainText("checkout");
    await expect(carried).toContainText("payments");
    // 3 hops, because on ambient the dependency crosses ztunnel twice.
    await expect(carried).toContainText("3");
    await expect(carried).not.toContainText("catalog");

    await page.getByRole("button", { name: "All proxies" }).click();
    await expect(page.getByTestId("mesh-proxies")).toBeVisible();
  });

  // A stale link must say what happened rather than render an empty page.
  test("says so when a linked proxy is not in the window", async ({ page }) => {
    await page.route("**/api/v1/mesh/proxies*", (r) => r.fulfill({ json: PROXIES }));
    await page.route("**/api/v1/mesh/control-plane*", (r) =>
      r.fulfill({ json: { available: false, state: "unconfigured" } }),
    );
    await page.goto("/mesh?proxy=long-gone");
    await expect(page.getByText("No proxy named long-gone in this window")).toBeVisible();
  });

  // The service map takes the hops OUT — that is its job. This tab is the only
  // place the mesh itself is drawn, scoped to the proxies and one step around
  // them so a large estate does not bury them.
  test("draws the mesh with its hops left in", async ({ page }) => {
    await page.route("**/api/v1/mesh/proxies*", (r) => r.fulfill({ json: PROXIES }));
    await page.route("**/api/v1/mesh/control-plane*", (r) =>
      r.fulfill({ json: { available: false, state: "unconfigured" } }),
    );
    await page.route("**/api/v1/service-map*", (r) =>
      r.fulfill({
        json: {
          services: [
            { name: "checkout", spanCount: 100, ratePerSec: 2, errorRate: 0, p50Ms: 1, p95Ms: 2, p99Ms: 3, namespace: "shop" },
            { name: "payments", spanCount: 90, ratePerSec: 2, errorRate: 0, p50Ms: 1, p95Ms: 2, p99Ms: 3, namespace: "shop" },
            { name: "global-waypoint.istio-waypoint", spanCount: 80, ratePerSec: 4, errorRate: 0, p50Ms: 1, p95Ms: 2, p99Ms: 3, role: "transport", namespace: "istio-waypoint" },
            // Touches no proxy: must stay out of a mesh graph.
            { name: "batch-loader", spanCount: 10, ratePerSec: 0.1, errorRate: 0, p50Ms: 1, p95Ms: 2, p99Ms: 3, namespace: "jobs" },
          ],
          edges: [
            { source: "checkout", target: "global-waypoint.istio-waypoint", calls: 210, errorCount: 0, errorRate: 0 },
            { source: "global-waypoint.istio-waypoint", target: "payments", calls: 208, errorCount: 0, errorRate: 0 },
            // The recovered dependency: drawn on the map, and NOT here, or the
            // same requests would be counted twice.
            { source: "checkout", target: "payments", calls: 210, errorCount: 0, errorRate: 0, collapsedCalls: 210, viaTransport: ["global-waypoint.istio-waypoint"] },
          ],
        },
      }),
    );
    await page.goto("/mesh");

    await page.getByRole("tab", { name: "Graph" }).click();
    await expect(page).toHaveURL(/view=graph/);
    await expect(page.getByTestId("mesh-graph")).toBeVisible();

    // Back to the table, and the tab state goes with it.
    await page.getByRole("tab", { name: "Proxies" }).click();
    await expect(page.getByTestId("mesh-proxies")).toBeVisible();
    await expect(page).not.toHaveURL(/view=graph/);
  });
});
