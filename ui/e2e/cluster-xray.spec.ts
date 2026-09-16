import { test, expect, type Page } from "@playwright/test";

const nodes = ["node-a", "node-b"].map(name => ({ name, cpuUsageCores: .5, memoryUsageBytes: 1024 ** 3,
  memoryAvailableBytes: 2 * 1024 ** 3, networkRxBytesPerSec: 100, networkTxBytesPerSec: 50, podCount: 1, cpuSeries: [], memorySeries: [] }));
const pods = [
  { name: "web", namespace: "shop", node: "node-a", workload: "frontend", cpuUsageCores: .42, memoryUsageBytes: 256 * 1024 ** 2 },
  { name: "web", namespace: "payments", node: "node-b", workload: "billing", cpuUsageCores: .2, memoryUsageBytes: 128 * 1024 ** 2 },
];
const source = { ...pods[0], service: "frontend" }, target = { ...pods[1], service: "billing" };
const connections = [
  { source, target, calls: 12, errors: 1, p95Ms: 24 },
  { source, target: { name: "", namespace: "", node: "", service: "" }, peer: "api.example.test", calls: 3, errors: 0, p95Ms: 8 },
];
async function fixtures(page: Page) {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.route("**/api/v1/infra/nodes*", route => route.fulfill({ json: { nodes } }));
  await page.route("**/api/v1/infra/pods?*", route => {
    const node = new URL(route.request().url()).searchParams.get("node");
    return route.fulfill({ json: { pods: node ? pods.filter(p => p.node === node) : pods } });
  });
  await page.route("**/api/v1/infra/pod-connections*", route => route.fulfill({ json: { connections, limit: 200, truncated: false } }));
}
async function choose(page: Page, label: string, name: string) {
  await page.getByRole("button", { name: label, exact: true }).click();
  await page.getByRole("option", { name, exact: true }).click();
}

test.describe("Cluster X-Ray", () => {
  // Software WebGL compilation competes with the authenticated suite on CI.
  test.describe.configure({ timeout: 60_000 });
  test("loads on demand and renders real seeded placement", async ({ page }) => {
    const connectionReads: string[] = [];
    page.on("request", req => { if (req.url().includes("/infra/pod-connections")) connectionReads.push(req.url()); });
    await page.goto("/nodes");
    await expect(page.getByRole("table").first()).toBeVisible();
    expect(connectionReads).toHaveLength(0);
    await page.getByRole("button", { name: "Cluster X-Ray", exact: true }).click();
    await expect(page.getByTestId("xray-canvas")).toBeVisible({ timeout: 15000 });
    await expect(page.locator(".xray-label").filter({ hasText: "seed-node-1" })).toBeVisible();
    await expect(page).toHaveURL(/view=xray/);
    await page.getByRole("button", { name: "Select Pod to inspect", exact: true }).click();
    await page.getByRole("option").filter({ hasText: "seed-checkout-0" }).click();
    await expect(page.getByTestId("xray-inspector")).toContainText("seed-checkout-0");
    await page.reload();
    await expect(page.getByTestId("xray-canvas")).toBeVisible({ timeout: 15000 });
    await expect(page.getByTestId("xray-inspector")).toContainText("seed-checkout-0");
  });

  test("keeps namespaces distinct and shows only observed connection measurements", async ({ page }) => {
    await fixtures(page); await page.goto("/nodes?view=xray");
    await choose(page, "Select Pod to inspect", "web shop · node-a");
    const inspector = page.getByTestId("xray-inspector");
    await expect(inspector).toContainText("0.420");
    await expect(inspector).toContainText("12 requests · p95 24ms");
    await expect(inspector).toContainText("api.example.test");
    await expect(inspector).toContainText("location unknown");
    await expect(inspector.getByRole("link", { name: "Explore service traces" })).toHaveAttribute("href", "/traces?service=frontend");
    await page.getByRole("button", { name: "Isolate neighbourhood" }).click();
    await expect(page).toHaveURL(/isolate=true/);
    await page.getByRole("button", { name: "Show all Pods" }).click();
    await choose(page, "Filter pods by namespace", "payments");
    await expect(page.getByText("Pod outside this view", { exact: true })).toBeVisible();
    await expect(page.locator(".xray-selected-label")).toBeHidden();
    await choose(page, "Select Pod to inspect", "web payments · node-b");
    await expect(inspector).toContainText("0.200");
    await expect(inspector).toContainText("billing");
    await page.reload();
    await expect(inspector).toContainText("node-b");
    await expect(page).toHaveURL(/ns=payments/);
  });

  test("supports layers, spacing, transparency, pause and camera controls without losing selection", async ({ page }, testInfo) => {
    await page.setViewportSize({ width: 1512, height: 1100 });
    const errors: string[] = []; page.on("pageerror", e => errors.push(e.message));
    await fixtures(page); await page.emulateMedia({ reducedMotion: "no-preference" }); await page.goto("/nodes?view=xray");
    await expect(page.getByTestId("xray-canvas")).toBeVisible({ timeout: 15000 });
    await choose(page, "Select Pod to inspect", "web shop · node-a");
    await expect(page.locator(".xray-selected-label")).toBeVisible();
    const labelBox = await page.locator(".xray-selected-label").boundingBox();
    // The label is above the actual mesh; clear the selection and click its
    // projected body to exercise raycasting rather than only the selector.
    await page.getByRole("button", { name: "Clear Pod selection" }).click();
    await page.mouse.click(labelBox!.x + labelBox!.width / 2, labelBox!.y + labelBox!.height + 24);
    await expect(page.getByTestId("xray-inspector")).toContainText("node-a");
    await page.getByTestId("cluster-xray").screenshot({ path: testInfo.outputPath("cluster-xray-desktop.png") });
    await page.getByLabel("Pods", { exact: true }).uncheck();
    await expect(page.locator(".xray-selected-label")).toBeHidden();
    await page.getByLabel("Pods", { exact: true }).check();
    await expect(page.locator(".xray-selected-label")).toBeVisible();
    for (const label of ["Nodes", "Logical infrastructure"]) {
      await page.getByRole("checkbox", { name: label, exact: true }).uncheck(); await page.getByRole("checkbox", { name: label, exact: true }).check();
    }
    await page.getByRole("slider", { name: "Spacing" }).fill("0");
    await page.getByRole("slider", { name: "X-Ray transparency" }).fill("90");
    await page.getByRole("button", { name: "Pause flows" }).click();
    await expect(page.getByRole("button", { name: "Animate flows" })).toHaveAttribute("aria-pressed", "true");
    for (const name of ["Zoom in", "Zoom out", "Reset camera"]) await page.getByRole("button", { name, exact: true }).click();
    await expect(page.getByTestId("xray-inspector")).toContainText("node-a");
    expect(errors).toEqual([]);
  });

  test("reports API errors and missing connections without losing placement", async ({ page }) => {
    await fixtures(page);
    await page.route("**/api/v1/infra/pod-connections*", route => route.fulfill({ status: 500, json: { error: "unavailable" } }));
    await page.goto("/nodes?view=xray");
    await expect(page.getByTestId("xray-canvas")).toBeVisible({ timeout: 15000 });
    await expect(page.getByText("Connections unavailable.", { exact: true })).toBeVisible({ timeout: 15000 });
    await page.route("**/api/v1/infra/pod-connections*", route => route.fulfill({ json: { connections: [], limit: 200, truncated: false } }));
    await page.getByRole("button", { name: "Retry connections" }).click();
    await expect(page.getByText("0 connections in view", { exact: true })).toBeVisible();
    await choose(page, "Select Pod to inspect", "web shop · node-a");
    await expect(page.getByTestId("xray-inspector")).toContainText("No trace-backed connections");
  });

  test("falls back to the inventory without WebGL", async ({ page }) => {
    await fixtures(page);
    await page.addInitScript(() => {
      const original = HTMLCanvasElement.prototype.getContext;
      HTMLCanvasElement.prototype.getContext = function (this: HTMLCanvasElement, ...args: Parameters<typeof original>) {
        if (String(args[0]).startsWith("webgl")) return null;
        return original.apply(this, args);
      } as typeof original;
    });
    await page.goto("/nodes?view=xray");
    await expect(page.getByText("3D is unavailable in this browser. Your inventory is still available.")).toBeVisible();
    await page.getByRole("button", { name: "Open inventory", exact: true }).click();
    await expect(page.getByRole("table").last()).toContainText("frontend");
  });

  test("bounds the scene and keeps omitted Pods inspectable", async ({ page }) => {
    await fixtures(page);
    const many = Array.from({ length: 30 }, (_, i) => ({ ...pods[0], name: `pod-${String(i).padStart(2, "0")}` }));
    await page.route("**/api/v1/infra/pods?*", route => route.fulfill({ json: { pods: many } }));
    await page.goto("/nodes?view=xray");
    await expect(page.getByText(/12 of 30 loaded Pods/)).toBeVisible();
    await choose(page, "Select Pod to inspect", "pod-29 shop · node-a");
    await expect(page.getByTestId("xray-inspector")).toContainText("outside the scene limit");
    await page.getByTestId("pod-filter").fill("pod-29");
    await expect(page.getByRole("button", { name: "Isolate neighbourhood" })).toBeEnabled();
    await expect(page.locator(".xray-selected-label")).toHaveText("pod-29");
  });

  test("respects reduced motion and fits mobile", async ({ page }, testInfo) => {
    await page.emulateMedia({ reducedMotion: "reduce" });
    await page.setViewportSize({ width: 390, height: 844 });
    await fixtures(page); await page.goto("/nodes?view=xray");
    await expect(page.getByTestId("xray-canvas")).toBeVisible({ timeout: 15000 });
    await expect(page.getByRole("button", { name: "Animate flows" })).toBeDisabled();
    await choose(page, "Select Pod to inspect", "web shop · node-a");
    await expect(page.getByTestId("xray-inspector")).toContainText("node-a");
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.screenshot({ path: testInfo.outputPath("cluster-xray-mobile.png"), fullPage: true });
  });
});
