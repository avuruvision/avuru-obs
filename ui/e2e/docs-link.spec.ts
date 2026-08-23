import { test, expect } from "@playwright/test";

// Every screen should answer "what am I looking at?" without a search. The
// link comes from the same nav model the sidebar and breadcrumbs use, so these
// assertions are really about that model staying in step with the docs site.
const DOCS = "https://avuruobs.io/docs";

test.describe("contextual documentation", () => {
  test("each screen links to the page that explains it", async ({ page }) => {
    for (const [route, expected] of [
      ["/traces", `${DOCS}/signals/traces`],
      ["/logs", `${DOCS}/signals/logs`],
      ["/health", `${DOCS}/signals/service-health`],
      ["/metrics", `${DOCS}/signals/metrics`],
    ] as const) {
      await page.goto(route);
      const link = page.getByTestId("docs-link");
      await expect(link).toBeVisible();
      await expect(link).toHaveAttribute("href", expected);
    }
  });

  test("opens in a new tab, and cannot reach back into the app", async ({ page }) => {
    await page.goto("/traces");
    const link = page.getByTestId("docs-link");
    await expect(link).toHaveAttribute("target", "_blank");
    // noopener matters: the docs are a different origin, and a tab opened
    // without it can navigate its opener.
    await expect(link).toHaveAttribute("rel", /noopener/);
  });

  test("a route with no documented page shows nothing rather than a guess", async ({ page }) => {
    // A link into a page that does not exist is worse than no link, so the
    // model omits `docs` and the header renders empty.
    await page.goto("/traces?trace=aaaa1111bbbb2222cccc3333dddd4444");
    await expect(page.getByTestId("docs-link")).toBeVisible();

    await page.goto("/login");
    await expect(page.getByTestId("docs-link")).toHaveCount(0);
  });
});
