import { test, expect } from "@playwright/test";

const selector = "Inspect a service or dependency";

test.describe("Explorer workspace", () => {
  test("opens on the map and carries shared entry context", async ({ page }) => {
    await page.goto("/?range=1h&selected=seed-checkout");
    await expect(page).toHaveURL(/\/service-map\?/);
    await expect(page).toHaveURL(/range=1h/);
    await expect(page.getByTestId("map-inspector")).toContainText("seed-checkout");
    await expect(page.getByRole("heading", { level: 1 })).toHaveText("Nothing runs alone.");
  });

  test("selection survives reload and leads to connected signals", async ({ page }) => {
    await page.goto("/service-map?range=1h");
    await page.getByRole("combobox", { name: selector }).selectOption("seed-checkout");
    await expect(page).toHaveURL(/selected=seed-checkout/);
    await page.reload();
    const inspector = page.getByTestId("map-inspector");
    await expect(inspector).toContainText("seed-checkout");
    await expect(inspector.getByTestId("inspector-latency")).not.toHaveText("—");
    await inspector.getByRole("link", { name: "Explore traces" }).click();
    await expect(page).toHaveURL(/\/traces\?/);
    await expect(page).toHaveURL(/service=seed-checkout/);
    await expect(page).toHaveURL(/range=1h/);
  });

  test("log actions use the service-aware log view", async ({ page }) => {
    await page.goto("/service-map?range=1h&selected=seed-checkout");
    await page.getByTestId("map-inspector").getByRole("link", { name: "Read logs" }).click();
    await expect(page).toHaveURL(/\/services\?/);
    await expect(page).toHaveURL(/service=seed-checkout/);
    await expect(page).toHaveURL(/view=logs/);
    await expect(page).toHaveURL(/range=1h/);
    await expect(page.getByRole("tab", { name: "Logs", exact: true })).toHaveAttribute("aria-selected", "true");
  });

  test("inferred targets show caller evidence, never own-service RED", async ({ page }) => {
    await page.goto("/service-map");
    const select = page.getByRole("combobox", { name: selector });
    await expect(select.locator("option").filter({ hasText: "inferred" }).first()).toBeAttached();
    const value = await select.locator("option").filter({ hasText: "inferred" }).first().getAttribute("value");
    await select.selectOption(value!);
    const inspector = page.getByTestId("map-inspector");
    await expect(inspector).toContainText("Inferred dependency");
    await expect(inspector.getByTestId("inspector-latency")).toHaveText("—");
    await expect(inspector.getByRole("link", { name: "Open service" })).toHaveCount(0);
    await expect(inspector.getByRole("link", { name: "Inspect caller traces" }).first()).toBeVisible();
  });

  test("does not silently replace an unavailable selection", async ({ page }) => {
    await page.goto("/service-map?selected=not-a-real-service");
    await expect(page.getByRole("heading", { name: "Selection outside this view" })).toBeVisible();
    await page.getByRole("button", { name: "Clear selection", exact: true }).click();
    await expect(page).not.toHaveURL(/selected=/);
  });

  test("empty collection teaches both supported connection paths", async ({ page }) => {
    await page.route("**/api/v1/service-map*", route => route.fulfill({ json: { services: [], edges: [] } }));
    await page.goto("/service-map");
    await expect(page.getByRole("region", { name: "First connection" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Discover with eBPF" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Bring OpenTelemetry" })).toBeVisible();
  });

  test("a failed read is not an empty installation", async ({ page }) => {
    await page.route("**/api/v1/service-map*", route => route.fulfill({ status: 503, json: { error: "unavailable" } }));
    await page.goto("/service-map");
    await expect(page.getByRole("heading", { name: "Unable to load service map" })).toBeVisible();
    await expect(page.getByRole("region", { name: "First connection" })).toHaveCount(0);
    await page.unroute("**/api/v1/service-map*");
    await page.getByRole("button", { name: "Retry service map" }).click();
    await expect(page.getByTestId("service-map")).toBeVisible();
  });

  test("core-only installs do not expose optional log actions", async ({ page }) => {
    await page.route("**/api/v1/capabilities", route => route.fulfill({ json: { version: "test", modules: ["core"] } }));
    await page.goto("/service-map?selected=seed-checkout");
    const inspector = page.getByTestId("map-inspector");
    await expect(inspector.getByRole("link", { name: "Open service" })).toBeVisible();
    await expect(inspector.getByRole("link", { name: "Read logs" })).toHaveCount(0);
    await expect(inspector).toContainText("Unknown");
    await expect(page.getByRole("checkbox", { name: "Carbon", exact: true })).toHaveCount(0);
  });

  test("mobile navigation keeps project controls and every enabled route reachable", async ({ page }) => {
    await page.setViewportSize({ width: 390, height: 844 });
    await page.emulateMedia({ reducedMotion: "reduce" });
    await page.goto("/service-map?selected=seed-checkout");
    await expect(page.getByTestId("map-inspector")).toBeVisible();
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    const open = page.getByRole("button", { name: "Open navigation" });
    await open.click();
    const dialog = page.getByRole("dialog", { name: "Navigation" });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByRole("button", { name: "Switch project" })).toBeVisible();
    await dialog.getByRole("link", { name: "Logs", exact: true }).click();
    await expect(page).toHaveURL(/\/logs/);
    await expect(dialog).toHaveCount(0);
    await open.click();
    await page.keyboard.press("Escape");
    await expect(dialog).toHaveCount(0);
    await expect(open).toBeFocused();
  });
});
