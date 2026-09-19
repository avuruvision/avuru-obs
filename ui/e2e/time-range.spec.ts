import { test, expect } from "@playwright/test";

// The global picker's absolute window: two local date-times, carried in the
// URL as ?range=custom&from=&to= and bridged across navigations like a preset.
test.describe("absolute time range", () => {
  test("applies an absolute window, sends it to the hub and keeps it across pages", async ({ page }) => {
    const asked: string[] = [];
    await page.route("**/api/v1/logs/services*", (route) => route.fulfill({ json: { services: [], workloads: [] } }));
    await page.route(/\/api\/v1\/logs(?:\?.*)?$/, (route) => {
      asked.push(route.request().url());
      return route.fulfill({ json: { logs: [], resolutions: [], nextCursor: "" } });
    });
    await page.goto("/logs");

    await page.getByRole("button", { name: "Custom time range" }).click();
    const form = page.getByRole("form", { name: "Absolute time range" });
    await form.getByLabel("Range start").fill("2026-09-17T10:00");
    await form.getByLabel("Range end").fill("2026-09-17T11:30");
    await form.getByRole("button", { name: "Apply" }).click();

    await expect(page).toHaveURL(/range=custom&from=2026-09-17T[^&]+&to=2026-09-17T/);
    await expect(page.getByRole("button", { name: "Custom time range" })).toHaveAttribute("aria-pressed", "true");
    await expect(page.getByRole("button", { name: "15m", exact: true })).toHaveAttribute("aria-pressed", "false");
    await expect.poll(() => asked.at(-1) ?? "").toMatch(/start=2026-09-17T.*end=2026-09-17T/);

    // A sidebar link is a bare path: the window follows through localStorage.
    await page.goto("/traces");
    await expect(page).toHaveURL(/range=custom&from=2026-09-17T/);

    // A preset takes over cleanly.
    await page.getByRole("button", { name: "1h", exact: true }).click();
    await expect(page).toHaveURL(/range=1h/);
    await expect(page).not.toHaveURL(/from=/);
  });

  test("refuses a window that runs backwards", async ({ page }) => {
    await page.goto("/logs");
    await page.getByRole("button", { name: "Custom time range" }).click();
    const form = page.getByRole("form", { name: "Absolute time range" });
    await form.getByLabel("Range start").fill("2026-09-17T12:00");
    await form.getByLabel("Range end").fill("2026-09-17T11:00");
    await form.getByRole("button", { name: "Apply" }).click();
    await expect(form.getByRole("alert")).toContainText("The end must come after the start");
    await expect(page).not.toHaveURL(/range=custom/);
  });

  test("cannot follow a log panel over a window that ended", async ({ page }) => {
    await page.route("**/api/v1/logs/services*", (route) => route.fulfill({ json: { services: [], workloads: [] } }));
    await page.route(/\/api\/v1\/logs(?:\?.*)?$/, (route) => route.fulfill({ json: { logs: [], resolutions: [], nextCursor: "" } }));
    await page.goto("/logs?services=checkout-api&display=panels&range=custom&from=2026-09-17T08%3A00%3A00.000Z&to=2026-09-17T09%3A30%3A00.000Z");
    const checkout = page.getByRole("region", { name: "Logs for checkout-api" });
    await expect(checkout.getByRole("button", { name: "Follow" })).toBeDisabled();
  });
});
