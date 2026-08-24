import { test, expect } from "@playwright/test";

// The layered sidebar (v0.8). Sections are LAYERS, each answering a different
// question, ordered the way an investigation runs: what is out there → what is
// it doing → what needs me → what it runs on.
//
// Until v0.8 nine of the thirteen entries sat under one "Observe" heading. That
// is a list, not a structure: it grew with every module and said nothing about
// how the screens relate.
test.describe("navigation", () => {
  test("groups screens by the question they answer", async ({ page }) => {
    await page.goto("/dashboard");
    const nav = page.getByRole("navigation", { name: "Primary" });

    // Order matters — it IS the claim this structure makes.
    const headings = await nav
      .locator("p")
      .evaluateAll((els) => els.map((e) => e.textContent?.trim()));
    expect(headings).toEqual([
      "Overview",
      "Topology",
      "Signals",
      "Operations",
      "Infrastructure",
      "System",
    ]);
  });

  test("the wedge's first click is still one click", async ({ page }) => {
    // The product promise is a live service map minutes after install. Whatever
    // the sidebar grows into, the map stays reachable from the landing route
    // without opening anything first.
    await page.goto("/dashboard");
    const nav = page.getByRole("navigation", { name: "Primary" });
    await nav.getByRole("link", { name: "Service Map", exact: true }).click();
    await expect(page).toHaveURL(/\/service-map/);
  });

  test("breadcrumbs name the layer a screen belongs to", async ({ page }) => {
    await page.goto("/traces");
    await expect(page.getByLabel("Breadcrumb")).toContainText("Signals");

    await page.goto("/green");
    await expect(page.getByLabel("Breadcrumb")).toContainText("Infrastructure");
  });

  test("a section with nothing left in it does not render an empty heading", async ({ page }) => {
    // With only core active, every entry under Operations belongs to a module
    // this install does not run — so the heading goes with them rather than
    // labelling a gap.
    await page.route("**/api/v1/capabilities", (route) =>
      route.fulfill({ json: { version: "test", modules: ["core"] } }),
    );
    await page.goto("/dashboard");

    const nav = page.getByRole("navigation", { name: "Primary" });
    await expect(nav.getByText("Operations", { exact: true })).toHaveCount(0);
    await expect(nav.getByText("Infrastructure", { exact: true })).toHaveCount(0);
    // The layers that survive are the ones with core screens in them.
    await expect(nav.getByText("Topology", { exact: true })).toBeVisible();
    await expect(nav.getByText("Signals", { exact: true })).toBeVisible();
  });
});
