import { test, expect } from "@playwright/test";

// The compose stack runs every module, so a module-off install is simulated by
// stubbing the capabilities endpoint — this exercises the UI's gating logic
// (sidebar filter + ModuleGate) without standing up a second stack.
const CAPABILITIES = "**/api/v1/capabilities";

const MODULE_LINKS = ["Logs", "Profiling", "Nodes", "Green"];

test.describe("module gating", () => {
  test("shows every module entry on a full install", async ({ page }) => {
    await page.goto("/traces");
    const nav = page.getByRole("navigation", { name: "Primary" });
    for (const label of MODULE_LINKS) {
      await expect(nav.getByRole("link", { name: label })).toBeVisible();
    }
  });

  test("hides inactive modules from the sidebar", async ({ page }) => {
    await page.route(CAPABILITIES, (route) =>
      route.fulfill({ json: { version: "test", modules: ["core"] } }),
    );

    await page.goto("/traces");
    const nav = page.getByRole("navigation", { name: "Primary" });

    // Core entries stay — the wedge is never gated. The Dashboard is core AND
    // the landing route, so it must survive a core-only install.
    await expect(nav.getByRole("link", { name: "Dashboard" })).toBeVisible();
    await expect(nav.getByRole("link", { name: "Service Map" })).toBeVisible();
    await expect(nav.getByRole("link", { name: "Traces" })).toBeVisible();
    // RED is trace-derived, so Metrics is core too.
    await expect(nav.getByRole("link", { name: "Metrics" })).toBeVisible();

    for (const label of MODULE_LINKS) {
      await expect(nav.getByRole("link", { name: label })).toHaveCount(0);
    }
  });

  test("explains how to enable the module on direct navigation", async ({ page }) => {
    await page.route(CAPABILITIES, (route) =>
      route.fulfill({ json: { version: "test", modules: ["core", "infra-metrics"] } }),
    );

    await page.goto("/logs");
    await expect(page.getByRole("heading", { name: /Logs module is off/ })).toBeVisible();
    await expect(page.getByText("modules.logs.enabled=true")).toBeVisible();

    await page.goto("/green");
    await expect(
      page.getByRole("heading", { name: /Energy & carbon module is off/ }),
    ).toBeVisible();
    await expect(page.getByText("modules.green.enabled=true")).toBeVisible();

    // An active module still renders its screen, not the notice.
    await page.goto("/nodes");
    await expect(page.getByRole("heading", { name: /module is off/ })).toHaveCount(0);
  });
});
