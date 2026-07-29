import { test, expect, type Page } from "@playwright/test";

// Per-project isolation in the UI. Seeded fixtures: `seed-checkout` in the
// default project, `seed-checkout-staging` under the staging tenant
// (avuru.tenant resource attribute). NOTE: the default service name is a
// PREFIX of the staging one — absence checks for it need the lookahead.
const STAGING_SVC = "seed-checkout-staging";
const DEFAULT_SVC_ONLY = /seed-checkout(?!-staging)/;

test.describe("project switcher (seeded data)", () => {
  test("default project shows only default-tenant data", async ({ page }) => {
    await page.goto("/traces");
    await expect(page.getByText(DEFAULT_SVC_ONLY).first()).toBeVisible();
    await expect(page.getByText(STAGING_SVC)).toHaveCount(0);
    // Switcher renders in the sidebar footer showing the active project.
    await expect(page.getByRole("button", { name: "Switch project" })).toContainText("default");
  });

  test("switching to staging swaps data and marks the URL", async ({ page }) => {
    await page.goto("/traces");
    await expect(page.getByText(DEFAULT_SVC_ONLY).first()).toBeVisible();

    await page.getByRole("button", { name: "Switch project" }).click();
    await page.getByRole("option", { name: "staging" }).click();

    await expect(page).toHaveURL(/project=staging/);
    await expect(page.getByText(STAGING_SVC).first()).toBeVisible();
    // Cross-project leakage check: no default-only service text anywhere.
    await expect(page.getByText(DEFAULT_SVC_ONLY)).toHaveCount(0);
  });

  test("?project= is shareable and survives navigation + reload", async ({ page }) => {
    await page.goto("/traces?project=staging&tab=traces");
    await expect(page.getByText(STAGING_SVC).first()).toBeVisible();

    // In-app navigation keeps the project (localStorage bridge, then the
    // provider re-materializes the param for shareability).
    await page.getByRole("link", { name: "Logs", exact: true }).click();
    await expect(page).toHaveURL(/project=staging/);

    await page.reload();
    await expect(page).toHaveURL(/project=staging/);
    await expect(page.getByRole("button", { name: "Switch project" })).toContainText("staging");
  });
});

// Admin project management (create/rename/delete). Admin-only, so this runs on
// the auth-enabled `make e2e-ui` stack; sign in as the bootstrap admin first
// (same identity as auth.spec.ts).
test.describe("project management (admin)", () => {
  const ADMIN = { email: "admin", password: "e2e-admin-pw" };

  async function signInAdmin(page: Page): Promise<void> {
    await page.goto("/login");
    await page.getByLabel("Email").fill(ADMIN.email);
    await page.getByLabel("Password").fill(ADMIN.password);
    await page.getByRole("button", { name: "Sign in" }).click();
    await expect(page).not.toHaveURL(/\/login/);
  }

  test("create, rename, then delete a project", async ({ page }) => {
    await signInAdmin(page);
    await page.goto("/settings");

    await page.getByRole("button", { name: "New project" }).click();
    await page.getByLabel("Project id (immutable)").fill("e2e-proj");
    await page.getByLabel("Display name").fill("E2E Project");
    await page.getByRole("button", { name: "Create", exact: true }).click();

    // Switcher now shows the new project's label as active.
    await expect(page.getByRole("button", { name: "Switch project" })).toContainText("E2E Project");

    // Rename the label.
    await page.getByLabel("Display name").fill("E2E Renamed");
    await page.getByRole("button", { name: "Save", exact: true }).click();
    await expect(page.getByRole("button", { name: "Switch project" })).toContainText("E2E Renamed");

    // Delete → falls back to default.
    await page.getByRole("button", { name: "Delete", exact: true }).click();
    await expect(page.getByRole("button", { name: "Switch project" })).toContainText("default");
  });

  test("the default project shows the read-only banner", async ({ page }) => {
    await signInAdmin(page);
    await page.goto("/settings");
    await expect(
      page.getByText(/defined through deployment configuration/),
    ).toBeVisible();
  });
});
