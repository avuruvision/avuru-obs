import { test, expect } from "@playwright/test";

// Seeded fixtures (deploy/compose/seed/fixtures) + the compose groups config
// (deploy/compose/groups.json): seed-payments is a healthy T0 group "payments";
// seed-checkout's entry span errors, so it reads down in its namespace-auto
// group. minSampleCount is lowered to 1 in the compose config so the tiny seed
// volume produces real statuses instead of "idle".

test.describe("service health board (seeded data)", () => {
  test("renders tier lanes, groups and members from real data", async ({ page }) => {
    await page.goto("/health");

    // Overall header + the T0 lane (the config group's tier).
    await expect(page.getByText(/Overall:/)).toBeVisible();
    await expect(page.getByRole("heading", { name: "Tier 0" })).toBeVisible();

    // Both seeded services surface as member chips (scope to the chip button —
    // a group's reason line can also mention the service name).
    await expect(page.getByRole("button", { name: "seed-payments" })).toBeVisible();
    await expect(page.getByRole("button", { name: "seed-checkout" })).toBeVisible();
  });

  test("opens the member detail with base status and RED evidence", async ({ page }) => {
    await page.goto("/health");

    await page.getByRole("button", { name: /seed-checkout/ }).first().click();
    await expect(page).toHaveURL(/member=seed-checkout/);

    // The detail panel shows the base status and the RED evidence, no 2nd call.
    await expect(page.getByText("Base")).toBeVisible();
    await expect(page.getByText("p95")).toBeVisible();
  });

  test("gates the board when the module is off", async ({ page }) => {
    await page.route("**/api/v1/capabilities", (route) =>
      route.fulfill({ json: { version: "test", modules: ["core"] } }),
    );

    // Hidden from the sidebar…
    await page.goto("/traces");
    const nav = page.getByRole("navigation", { name: "Primary" });
    await expect(nav.getByRole("link", { name: "Service Health" })).toHaveCount(0);

    // …and direct navigation explains how to turn it on.
    await page.goto("/health");
    await expect(page.getByRole("heading", { name: /Service health module is off/ })).toBeVisible();
    await expect(page.getByText("modules.serviceHealth.enabled=true")).toBeVisible();
  });
});
