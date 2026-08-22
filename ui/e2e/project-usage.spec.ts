import { test, expect, type Page } from "@playwright/test";

// Per-project storage usage on Settings → Storage. Route-stubbed like
// projects-admin.spec.ts, so it runs without a stack: what is under test is
// that the page tells a project's footprint apart from the install's, and that
// an aggregate says so instead of passing a union off as one cluster's data.

async function stubAdmin(page: Page) {
  await page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({
      json: {
        user: { id: "admin", email: "admin", name: "Admin", anonymous: false },
        grants: [{ scope: "*", role: "admin" }],
      },
    }),
  );
}

type ProjectUsage = {
  id: string;
  tenants: string[];
  retentionVaries?: boolean;
  signals: Array<{
    signal: string;
    rows: number;
    estimatedBytes: number;
    oldest?: string;
    rowsPerMinute: number;
    retentionDays: number;
    inherited: boolean;
  }>;
};

async function stubStatus(page: Page, project?: ProjectUsage) {
  await page.route("**/api/v1/system/status", (route) =>
    route.fulfill({
      json: {
        version: "test",
        overall: "healthy",
        checkedAt: new Date(0).toISOString(),
        components: [],
        signals: [
          {
            signal: "traces",
            rows: 1000,
            bytes: 40960,
            compressedBytes: 10240,
            compression: 4,
            retentionDays: 7,
            ttlDays: 7,
          },
        ],
        disks: [],
        project,
      },
    }),
  );
}

test.describe("per-project storage usage", () => {
  test("a project's own footprint sits beside the install's", async ({ page }) => {
    await stubAdmin(page);
    await stubStatus(page, {
      id: "staging",
      tenants: ["staging"],
      signals: [
        {
          signal: "traces",
          rows: 40,
          estimatedBytes: 4096,
          oldest: new Date(Date.now() - 3 * 3600_000).toISOString(),
          rowsPerMinute: 2.5,
          retentionDays: 3,
          inherited: false,
        },
      ],
    });

    await page.goto("/settings?tab=storage&project=staging");
    await expect(page.getByText("This project — staging")).toBeVisible();
    const row = page.getByTestId("project-usage").locator("tbody tr").first();
    await expect(row).toContainText("40");
    await expect(row).toContainText("2.5/min");
    // The window that actually applies to this project, and where it came from.
    await expect(row).toContainText("3d");
    await expect(row).toContainText("(own)");
    // The install-wide table is still there, with its own (larger) numbers.
    await expect(page.getByTestId("storage-usage")).toContainText("1,000");
  });

  test("an aggregate names its members and refuses to average their windows", async ({ page }) => {
    await stubAdmin(page);
    await stubStatus(page, {
      id: "estate",
      tenants: ["prod-eu", "prod-us"],
      retentionVaries: true,
      signals: [
        {
          signal: "traces",
          rows: 90,
          estimatedBytes: 9216,
          rowsPerMinute: 0,
          retentionDays: 0,
          inherited: true,
        },
      ],
    });

    await page.goto("/settings?tab=storage&project=estate");
    await expect(page.getByText("aggregate of 2")).toBeVisible();
    await expect(page.getByText(/Union of prod-eu, prod-us/)).toBeVisible();
    await expect(page.getByTestId("project-usage")).toContainText("varies");
  });
});
