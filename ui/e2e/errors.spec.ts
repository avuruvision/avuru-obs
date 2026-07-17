import { test, expect } from "@playwright/test";

test.describe("errors screen (seeded data)", () => {
  test("lists derived issues", async ({ page }) => {
    await page.goto("/errors");
    await expect(page.getByRole("tab", { name: "Unresolved", exact: true })).toBeVisible();
    await expect(page.getByText("java.lang.NullPointerException").first()).toBeVisible();
  });

  test("opens an issue, shows the stack trace, and triages it", async ({ page }) => {
    await page.goto("/errors");
    await page.getByText("java.lang.NullPointerException").first().click();
    await expect(page.getByText("Latest stack trace")).toBeVisible();
    await expect(page.getByText(/Cart\.java/).first()).toBeVisible();
    await page.getByRole("button", { name: "Resolve" }).click();
    await expect(page.getByText("Resolved").first()).toBeVisible();
  });

  test("issue rows link back to the originating trace", async ({ page }) => {
    // ?status=all: the triage test above may have resolved this issue, and
    // specs in a file share one worker — don't depend on the default tab.
    await page.goto("/errors?status=all");
    await page.getByText("java.lang.NullPointerException").first().click();
    const traceLink = page.getByRole("link", { name: /originating trace/ });
    await expect(traceLink).toBeVisible();
    await expect(traceLink).toHaveAttribute("href", /\/traces\?trace=/);
  });
});
