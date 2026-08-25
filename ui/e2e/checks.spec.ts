import { test, expect } from "@playwright/test";

// Endpoint checks on the health board, stubbed at the API.
//
// The compose stack has no probe target and no scheduler running, and the
// contract under test is what the board SAYS about a group nobody is calling —
// which is the whole point of the feature.
function boardWith(checks: unknown[], groupStatus: string, spanCount = 0) {
  return {
    overall: groupStatus,
    checkedAt: new Date().toISOString(),
    window: { start: new Date(Date.now() - 3600_000).toISOString(), end: new Date().toISOString() },
    groups: [
      {
        name: "core",
        tier: "T0",
        source: "config",
        tierSource: "config",
        status: groupStatus,
        reason: "no traffic, but endpoint checks are passing",
        counts: {},
        spanCount,
        ratePerSec: 0,
        errorRate: 0,
        p95Ms: 0,
        members: [],
        checks,
      },
    ],
  };
}

test.describe("endpoint checks on the health board", () => {
  test("a silent group with a passing check reads healthy, not unknown", async ({ page }) => {
    await page.route("**/api/v1/health/groups*", (r) =>
      r.fulfill({
        json: boardWith(
          [{ id: "core-login", ok: true, failing: false, consecutiveFailures: 0, latencyMs: 42 }],
          "healthy",
        ),
      }),
    );
    await page.goto("/health");

    const checks = page.getByTestId("group-checks").first();
    await expect(checks).toContainText("core-login");
    // The group has zero traffic in the window and is still reported healthy —
    // that verdict exists only because something probed it.
    await expect(page.getByTestId("group-card").first()).toContainText("Healthy");
  });

  test("a failing check links to the trace of the probe that failed", async ({ page }) => {
    await page.route("**/api/v1/health/groups*", (r) =>
      r.fulfill({
        json: boardWith(
          [
            {
              id: "core-login",
              ok: false,
              failing: true,
              consecutiveFailures: 2,
              error: "expected a 2xx, got 503",
              traceId: "abc123def456",
            },
          ],
          "down",
        ),
      }),
    );
    await page.goto("/health");

    const link = page.getByTestId("group-checks").first().getByRole("link", { name: /core-login/ });
    await expect(link).toHaveAttribute("href", "/traces/abc123def456");
  });

  test("a single failure is shown apart from a failing check", async ({ page }) => {
    await page.route("**/api/v1/health/groups*", (r) =>
      r.fulfill({
        json: boardWith(
          [
            {
              id: "core-login",
              ok: false,
              failing: false,
              consecutiveFailures: 1,
              error: "connection refused",
            },
          ],
          "idle",
        ),
      }),
    );
    await page.goto("/health");

    // One failure is a restart or a dropped packet. It is visible, and the
    // group has NOT moved — the distinction the two-in-a-row rule exists for.
    const pill = page.getByTestId("group-checks").first();
    await expect(pill).toContainText("core-login");
    await expect(page.getByTestId("group-card").first()).not.toContainText("Down");
  });

  test("a group with no checks shows no check row at all", async ({ page }) => {
    await page.route("**/api/v1/health/groups*", (r) => r.fulfill({ json: boardWith([], "idle") }));
    await page.goto("/health");
    await expect(page.getByTestId("group-checks")).toHaveCount(0);
  });
});
