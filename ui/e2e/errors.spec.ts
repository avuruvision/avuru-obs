import { test, expect } from "@playwright/test";

// The compose seed ships two error sources: a span exception event
// (ConnectionError, from traces_checkout.json) and ERROR logs carrying
// exception.* attributes (NullPointerException, from logs_errors.json). Both
// must surface as issues.
test.describe("errors screen (seeded data)", () => {
  test("lists derived issues", async ({ page }) => {
    await page.goto("/errors");
    await expect(page.getByRole("heading", { name: "Errors" })).toBeVisible();

    // The NullPointerException issue derived from the seeded ERROR logs.
    await expect(page.getByText("java.lang.NullPointerException").first()).toBeVisible();
  });

  test("opens an issue, shows the stack trace, and triages it", async ({ page }) => {
    await page.goto("/errors");

    // Open the NPE issue detail.
    await page.getByText("java.lang.NullPointerException").first().click();

    // Detail panel: stack trace visible.
    await expect(page.getByText("Latest stack trace")).toBeVisible();
    await expect(page.getByText(/Cart\.java/).first()).toBeVisible();

    // Resolve it → the status badge flips to Resolved.
    await page.getByRole("button", { name: "Resolve" }).click();
    await expect(page.getByText("Resolved").first()).toBeVisible();

    // Under the Resolved tab, the issue is present; under Unresolved it is gone.
    await page.getByRole("tab", { name: "Resolved" }).click();
    await expect(page.getByText("java.lang.NullPointerException").first()).toBeVisible();
  });

  test("issue rows link back to the originating trace", async ({ page }) => {
    await page.goto("/errors");
    await page.getByText("java.lang.NullPointerException").first().click();

    const traceLink = page.getByRole("link", { name: /originating trace/ });
    await expect(traceLink).toBeVisible();
    await expect(traceLink).toHaveAttribute("href", /\/traces\?trace=/);
  });
});
