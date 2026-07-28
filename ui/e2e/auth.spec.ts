import { test, expect, type Page } from "@playwright/test";

// Auth flows against the compose stack with auth ENABLED. `make e2e-ui` pins
// AVURUOPS_AUTH_ADMIN_PASSWORD=e2e-admin-pw, and the hub bootstraps a single
// admin whose identity is `admin` (hub/internal/auth/service.go). A missing
// session on any protected call bounces the browser to /login?next=… (see the
// global 401 handler in src/lib/api.ts).
//
// Selectors are derived from the real, label-based markup shipped by the login
// and users panels — NOT the placeholder-based draft in the plan:
//   • login form (app/login/page.tsx): wrapping <label>Email / <label>Password,
//     a submit <Button>Sign in</Button>, and a role="alert" that renders exactly
//     "Invalid email or password" on a 401.
//   • sign-out (src/components/layout/sidebar.tsx): a <button>Sign out</button>
//     that SidebarAuth shows once /auth/me reports a non-anonymous session.
//   • users panel (src/components/settings/users-panel.tsx): an "Add user"
//     button that toggles an inline form (Email / Name / Password / Project
//     scope / Role); created rows render the email in a table cell.
const ADMIN_EMAIL = "admin";
const ADMIN_PASSWORD = "e2e-admin-pw";

// Sign in from the login screen and wait until we've left it. Reused by the
// admin-only test; the redirect test inlines the same steps after asserting the
// bounce so it can cover the ?next= round-trip.
async function signIn(page: Page): Promise<void> {
  await page.goto("/login");
  await page.getByLabel("Email").fill(ADMIN_EMAIL);
  await page.getByLabel("Password").fill(ADMIN_PASSWORD);
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page).not.toHaveURL(/\/login/);
}

test.describe("auth", () => {
  test("redirects an anonymous visitor to /login, then signs in", async ({ page }) => {
    // Any protected page with no session hands off to the login screen.
    await page.goto("/");
    await expect(page).toHaveURL(/\/login/);

    await page.getByLabel("Email").fill(ADMIN_EMAIL);
    await page.getByLabel("Password").fill(ADMIN_PASSWORD);
    await page.getByRole("button", { name: "Sign in" }).click();

    // Lands back on the app (?next round-trips to "/") with a real session, so
    // the sidebar's Sign-out control is present.
    await expect(page).not.toHaveURL(/\/login/);
    await expect(page.getByRole("button", { name: "Sign out" })).toBeVisible();
  });

  test("rejects a wrong password with an inline error and stays on /login", async ({ page }) => {
    await page.goto("/login");
    await page.getByLabel("Email").fill(ADMIN_EMAIL);
    await page.getByLabel("Password").fill("wrong");
    await page.getByRole("button", { name: "Sign in" }).click();

    // The 401 branch renders this exact copy in a role="alert" <p>; navigation
    // never happens, so we're still on the login screen.
    await expect(page.getByText("Invalid email or password")).toBeVisible();
    await expect(page).toHaveURL(/\/login/);
  });

  test("an admin creates a user and sees the new row", async ({ page }) => {
    await signIn(page);
    await page.goto("/settings/users");

    // The reveal button ("Add user") and the form's submit button ("Add user")
    // are mutually exclusive — the panel renders one XOR the other on `adding` —
    // so this name resolves uniquely at each step.
    await page.getByRole("button", { name: "Add user", exact: true }).click();

    await page.getByLabel("Email").fill("pw-test@example.com");
    await page.getByLabel("Password").fill("pw-test-pw");
    await page.getByLabel("Project scope").fill("demo");
    await page.getByRole("button", { name: "Add user", exact: true }).click();

    // On success the panel reloads and the new user's email shows in a table cell.
    await expect(
      page.getByRole("cell", { name: "pw-test@example.com" }),
    ).toBeVisible();
  });
});
