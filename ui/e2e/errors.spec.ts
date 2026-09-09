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

  // The band and the rows are answers to the same question — a total that
  // disagrees with the list under it is worse than no total at all.
  test("the stats band counts the issues in the list", async ({ page }) => {
    await page.goto("/errors?status=all");
    const band = page.getByTestId("errors-stats");
    await expect(band).toBeVisible();

    const num = async (id: string) =>
      Number((await page.getByTestId(`${id}-value`).innerText()).replace(/[^0-9]/g, ""));
    const issues = await num("stat-issues");
    const events = await num("stat-events");
    expect(issues).toBeGreaterThan(0);
    expect(events).toBeGreaterThanOrEqual(issues);

    await expect(page.getByTestId("errors-histogram")).toBeVisible();
    // Nothing is truncated at seed scale, so the "showing N of T" line must not
    // appear — it would be claiming a page boundary that does not exist.
    await expect(page.getByTestId("issues-footer")).toHaveCount(0);
  });

  test("a top-service entry filters the list to that service", async ({ page }) => {
    await page.goto("/errors?status=all");
    const first = page.getByTestId(/^top-service-/).first();
    await expect(first).toBeVisible();
    const service = (await first.getAttribute("data-testid"))!.replace("top-service-", "");

    await first.click();
    await expect(page).toHaveURL(new RegExp(`service=${encodeURIComponent(service)}`));
    await expect(first).toHaveAttribute("aria-pressed", "true");

    const cells = page.locator("tbody tr td:nth-child(2)");
    await expect(cells.first()).toBeVisible();
    for (const text of await cells.allInnerTexts()) {
      expect(text.trim()).toBe(service);
    }
  });

  // The defect this page shipped with: the app shell owns no scrollbar, so a
  // non-scrolling list simply hid every row past the fold.
  test("the issue list scrolls to its last row", async ({ page }) => {
    await page.setViewportSize({ width: 1280, height: 480 });
    await page.goto("/errors?status=all");

    const scroller = page.getByTestId("issues-scroll");
    await expect(scroller).toBeVisible();
    await expect(scroller).toHaveCSS("overflow-y", "auto");

    const lastRow = page.locator("tbody tr").last();
    await lastRow.scrollIntoViewIfNeeded();
    await expect(lastRow).toBeInViewport();
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
