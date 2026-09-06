import { test, expect } from "@playwright/test";

// The mesh graph's legend, stubbed at the API like mesh.spec.ts. What is under
// test is what the graph SAYS about its shapes: it must name the roles it drew,
// and only those — a role in the proxy list with no node on the graph explains
// a shape nobody can see, and a proxy list without roles has nothing to explain.
const stat = { spanCount: 100, errorRate: 0, p50Ms: 1, p95Ms: 2, p99Ms: 3 };
const SERVICE_MAP = {
  services: [
    { name: "checkout", ratePerSec: 2, namespace: "shop", ...stat },
    { name: "payments", ratePerSec: 2, namespace: "shop", ...stat },
    {
      name: "global-waypoint.istio-waypoint",
      ratePerSec: 4,
      role: "transport",
      namespace: "istio-waypoint",
      ...stat,
    },
    { name: "ztunnel", ratePerSec: 4, role: "transport", namespace: "istio-system", ...stat },
  ],
  edges: [
    { source: "checkout", target: "ztunnel", calls: 210, errorCount: 0, errorRate: 0 },
    { source: "ztunnel", target: "global-waypoint.istio-waypoint", calls: 210, errorCount: 0, errorRate: 0 },
    { source: "global-waypoint.istio-waypoint", target: "payments", calls: 208, errorCount: 0, errorRate: 0 },
  ],
};

const proxy = { ratePerSec: 4, errorRate: 0, p50Ms: 1, p95Ms: 2, callsIn: 240, callsOut: 240 };
// A hub predating roles: the proxies, and nothing said about what they are.
const WITHOUT_ROLES = {
  proxies: [
    { name: "global-waypoint.istio-waypoint", namespace: "istio-waypoint", ...proxy },
    { name: "ztunnel", namespace: "istio-system", ...proxy },
    // In the list, not on the graph: no node in the service map carries it.
    { name: "istio-ingressgateway-istio.istio-edge", namespace: "istio-edge", ...proxy },
  ],
};
const ROLES: Record<string, string> = {
  "global-waypoint.istio-waypoint": "waypoint",
  ztunnel: "ztunnel",
  "istio-ingressgateway-istio.istio-edge": "ingress-gateway",
};
const WITH_ROLES = {
  proxies: WITHOUT_ROLES.proxies.map((p) => ({ ...p, role: ROLES[p.name] })),
};

async function stubMesh(page: import("@playwright/test").Page, proxies: unknown) {
  await page.route("**/api/v1/mesh/proxies*", (r) => r.fulfill({ json: proxies }));
  await page.route("**/api/v1/mesh/control-plane*", (r) =>
    r.fulfill({ json: { available: false, state: "unconfigured" } }),
  );
  await page.route("**/api/v1/service-map*", (r) => r.fulfill({ json: SERVICE_MAP }));
}

test.describe("mesh graph legend", () => {
  test("names the shape of every role it draws, and only those", async ({ page }) => {
    await stubMesh(page, WITH_ROLES);
    await page.goto("/mesh?view=graph");

    await expect(page.getByTestId("mesh-graph")).toBeVisible();
    const legend = page.getByTestId("mesh-graph-legend");
    // Exact labels from mesh-roles.ts ROLE_LABELS.
    await expect(legend).toContainText("Waypoint");
    await expect(legend).toContainText("ztunnel");
    await expect(legend).not.toContainText("Ingress gateway");
  });

  test("explains nothing when the proxies carry no role", async ({ page }) => {
    await stubMesh(page, WITHOUT_ROLES);
    await page.goto("/mesh?view=graph");

    await expect(page.getByTestId("mesh-graph")).toBeVisible();
    await expect(page.getByTestId("mesh-graph-legend")).toHaveCount(0);
  });
});
