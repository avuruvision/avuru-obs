import { test, expect, type Page } from "@playwright/test";

// Settings → Storage and Settings → Access. Self-contained via route
// interception, like projects-admin.spec.ts: both tabs render whatever the hub
// reports, so the interesting cases are the ones a live stack does not have —
// retention drift, and an install with authentication switched off.

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

function signal(name: string, retentionDays: number, ttlDays: number) {
  return {
    signal: name,
    rows: 1000,
    bytes: 10_000,
    compressedBytes: 1_000,
    compression: 10,
    oldest: new Date(Date.now() - 86_400_000).toISOString(),
    newest: new Date().toISOString(),
    retentionDays,
    ttlDays,
  };
}

async function stubSystemStatus(
  page: Page,
  body: Record<string, unknown> = {},
) {
  await page.route("**/api/v1/system/status", (route) =>
    route.fulfill({
      json: {
        version: "test",
        overall: "healthy",
        checkedAt: new Date().toISOString(),
        components: [{ name: "Hub", status: "healthy", detail: "test" }],
        signals: [signal("traces", 7, 7)],
        disks: [{ name: "default", freeBytes: 50e9, totalBytes: 100e9 }],
        connection: {
          address: "clickhouse:9000",
          database: "otel",
          username: "avuru",
          protocol: "native",
        },
        ...body,
      },
    }),
  );
}

async function stubPermissions(page: Page, authEnabled = true) {
  await page.route("**/api/v1/auth/permissions", (route) =>
    route.fulfill({
      json: {
        authEnabled,
        roles: [
          { role: "admin", label: "Admin", description: "Everything, plus configuration." },
          { role: "editor", label: "Editor", description: "Viewer plus operational writes." },
          { role: "viewer", label: "Viewer", description: "Reads granted projects." },
        ],
        areas: [
          { area: "traces", label: "Traces", read: "viewer" },
          { area: "errors", label: "Error tracking", read: "viewer", write: "editor" },
          { area: "users", label: "Users", read: "admin", write: "admin" },
        ],
      },
    }),
  );
}

test.describe("settings → storage", () => {
  test("shows where telemetry is stored, and says why it is read-only", async ({ page }) => {
    await stubAdmin(page);
    await stubSystemStatus(page);

    await page.goto("/settings?tab=storage");
    const conn = page.getByTestId("storage-connection");
    await expect(conn.getByText("clickhouse:9000")).toBeVisible();
    await expect(conn.getByText("otel", { exact: true })).toBeVisible();
    await expect(page.getByText(/can’t hold its own connection details/)).toBeVisible();
    await expect(page.getByText("--set clickhouse.address=…")).toBeVisible();
  });

  test("reports usage and disk capacity", async ({ page }) => {
    await stubAdmin(page);
    await stubSystemStatus(page);

    await page.goto("/settings?tab=storage");
    const usage = page.getByTestId("storage-usage");
    await expect(usage.getByText("traces")).toBeVisible();
    await expect(usage.getByText("10.0x")).toBeVisible();
    await expect(page.getByText(/free \//)).toBeVisible();
  });

  // The failure this column exists for: retention was changed in values but
  // the tables are still enforcing the old TTL, so the configured number is a
  // wish. Showing only the configured value would hide it.
  test("flags retention the tables are not enforcing", async ({ page }) => {
    await stubAdmin(page);
    await stubSystemStatus(page, {
      signals: [signal("traces", 30, 7), signal("logs", 3, 0)],
    });

    await page.goto("/settings?tab=storage");
    await expect(page.getByTestId("retention-drift-traces")).toContainText("30d → 7d");
    await expect(page.getByTestId("retention-drift-logs")).toContainText("3d → none");
  });

  test("no drift marker when the tables agree", async ({ page }) => {
    await stubAdmin(page);
    await stubSystemStatus(page);

    await page.goto("/settings?tab=storage");
    await expect(page.getByTestId("storage-usage").getByText("7d")).toBeVisible();
    await expect(page.getByTestId("retention-drift-traces")).toHaveCount(0);
  });
});

test.describe("settings → access", () => {
  test("renders the matrix the hub reports", async ({ page }) => {
    await stubAdmin(page);
    await stubPermissions(page);

    await page.goto("/settings?tab=access");
    await expect(page.getByTestId("role-list").getByText("Admin", { exact: true })).toBeVisible();

    // Read-only signal: every role reads it, none changes it.
    await expect(page.getByTestId("cell-traces-viewer")).toContainText("Read");
    await expect(page.getByTestId("cell-traces-admin")).not.toContainText("change");
    // Editor write: viewer reads, editor and admin change.
    await expect(page.getByTestId("cell-errors-viewer")).toContainText("Read");
    await expect(page.getByTestId("cell-errors-editor")).toContainText("change");
    await expect(page.getByTestId("cell-errors-admin")).toContainText("change");
    // Admin-only: a viewer cannot even read it. The cell is an icon, so assert
    // both its accessible name and the absence of any "Read" affordance —
    // an empty cell alone would also pass if the row failed to render.
    const viewerOnUsers = page.getByTestId("cell-users-viewer");
    await expect(viewerOnUsers.locator('[aria-label="no access"]')).toBeVisible();
    await expect(viewerOnUsers).not.toContainText("Read");
    await expect(page.getByTestId("cell-users-admin")).toContainText("change");
  });

  // A matrix that looks enforced on an install where nothing is enforced would
  // be worse than no matrix.
  test("says so when authentication is off", async ({ page }) => {
    await stubAdmin(page);
    await stubPermissions(page, false);

    await page.goto("/settings?tab=access");
    await expect(page.getByText(/Authentication is off on this install/)).toBeVisible();
    await expect(page.getByText("auth.enabled=true")).toBeVisible();
    // The matrix still renders — it describes what would apply.
    await expect(page.getByTestId("permission-matrix")).toBeVisible();
  });
});
