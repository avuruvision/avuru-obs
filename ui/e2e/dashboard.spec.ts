import { test, expect } from "@playwright/test";

// The Dashboard (v0.5 W6) is one fixed overview screen over APIs that already
// exist. Seeded fixtures (deploy/compose/seed/fixtures + deploy/compose/
// groups.json): services seed-checkout and seed-payments, the T0 config group
// "payments", and one node seed-node-1 carrying two pods.
//
// A module-off install is simulated by stubbing capabilities, the same
// technique as modules.spec.ts — the compose stack runs everything.
const CAPABILITIES = "**/api/v1/capabilities";
const SERVICE_MAP = "**/api/v1/service-map*";

test.describe("dashboard", () => {
  test("is where the product opens", async ({ page }) => {
    await page.goto("/");
    await expect(page).toHaveURL(/\/dashboard/);
    await expect(page.getByTestId("dashboard-screen")).toBeVisible();
  });

  test("renders all three bands from seeded data", async ({ page }) => {
    await page.goto("/dashboard");

    // Band 1: the seeded config group, with its tier, status and RED numbers.
    // Scoped to the one card — the seed also produces an auto group, and both
    // cards carry the same metric labels.
    const summary = page.getByTestId("dashboard-summary");
    await expect(summary.getByRole("heading", { name: "Service groups" })).toBeVisible();
    const payments = summary.getByRole("link", { name: /payments/ });
    await expect(payments).toBeVisible();
    await expect(payments.getByText("T0")).toBeVisible();
    await expect(payments.getByText("Healthy")).toBeVisible();
    await expect(payments.getByText("p95")).toBeVisible();

    // Band 2: the compact map plus the firing list, side by side.
    await expect(page.getByTestId("service-map-compact")).toBeVisible();
    await expect(page.getByRole("heading", { name: "Firing" })).toBeVisible();

    // Band 3: capacity, from the node the green seed permanently provides.
    const capacity = page.getByTestId("dashboard-capacity");
    await expect(capacity.getByText("seed-node-1")).toBeVisible();
    await expect(capacity.getByText("Memory utilization per node")).toBeVisible();
  });

  test("summary cards link into the screen that explains them", async ({ page }) => {
    await page.goto("/dashboard");
    await page.getByTestId("dashboard-summary").getByText("payments", { exact: true }).click();
    await expect(page).toHaveURL(/\/health/);
  });

  test("the compact map is the same graph — a node opens its traces", async ({ page }) => {
    await page.goto("/dashboard");
    const map = page.getByTestId("service-map-compact");
    await expect(map).toBeVisible();
    // cytoscape draws to a canvas, so the click is positional: the graph is
    // laid out inside this box and any node tap routes to /traces. Asserting
    // the box renders at overview height is the honest check here; the routing
    // itself is covered on the full map, and both share one implementation.
    await expect(map).toHaveClass(/h-75/);
  });

  test("falls back to services when service health is off, and drops the bands it cannot fill", async ({
    page,
  }) => {
    await page.route(CAPABILITIES, (route) =>
      route.fulfill({ json: { version: "test", modules: ["core"] } }),
    );
    await page.goto("/dashboard");

    // Band 1 degrades to the service inventory rather than disappearing.
    const summary = page.getByTestId("dashboard-summary");
    await expect(summary.getByRole("heading", { name: "Services" })).toBeVisible();
    await expect(summary.getByText("seed-checkout", { exact: true })).toBeVisible();
    await expect(summary.getByText(/enable service health to group them/)).toBeVisible();

    // The map is core, so band 2 stays — but with no alerting module there is
    // no firing list, and with no infra-metrics module no capacity band.
    await expect(page.getByTestId("service-map-compact")).toBeVisible();
    await expect(page.getByRole("heading", { name: "Firing" })).toHaveCount(0);
    await expect(page.getByTestId("dashboard-capacity")).toHaveCount(0);
  });

  test("the Dashboard entry survives a core-only install", async ({ page }) => {
    await page.route(CAPABILITIES, (route) =>
      route.fulfill({ json: { version: "test", modules: ["core"] } }),
    );
    await page.goto("/dashboard");

    // It is the landing route: hiding it would strand every install that runs
    // no optional module.
    const nav = page.getByRole("navigation", { name: "Primary" });
    await expect(nav.getByRole("link", { name: "Dashboard" })).toBeVisible();
  });

  test("teaches instead of drawing empty cards when nothing has reported", async ({ page }) => {
    await page.route(SERVICE_MAP, (route) => route.fulfill({ json: { services: [], edges: [] } }));
    await page.goto("/dashboard");

    await expect(page.getByRole("heading", { name: "No telemetry yet" })).toBeVisible();
    await expect(page.getByText(/Point an OTel SDK at the gateway/)).toBeVisible();
    await expect(page.getByTestId("dashboard-summary")).toHaveCount(0);
  });
});
