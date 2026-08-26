import { test, expect, type Page } from "@playwright/test";

// Auth flows against the compose stack with auth ENABLED. `make e2e-ui` pins
// AVURUOBS_AUTH_ADMIN_PASSWORD=e2e-admin-pw, and the hub bootstraps a single
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

// The one spec that must start SIGNED OUT: everything else in this suite opens
// with the shared admin session from global-setup.
test.use({ storageState: { cookies: [], origins: [] } });

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

// OIDC SSO on the login screen — OPT-IN: the default `make e2e-ui` stack has
// no IdP, so these run only when OIDC_E2E=1 against the `oidc-e2e` compose
// profile + override file (deploy/compose/docker-compose.oidc-e2e.yaml).
// The SSO control is an <a> (a full-page navigation to /api/v1/auth/oidc/start,
// NOT a fetch), hence getByRole("link").
test.describe("auth: oidc", () => {
  test.skip(!process.env.OIDC_E2E, "OIDC_E2E not set — mock-IdP stack not running");

  test("offers Sign in with SSO on the login page", async ({ page }) => {
    await page.goto("/login");
    await expect(
      page.getByRole("link", { name: "Sign in with SSO" }),
    ).toBeVisible();
  });

  test("forceSSO suppresses the password form — SSO only", async ({ page }) => {
    // Needs forceSSO: true in deploy/compose/oidc-e2e.yaml (shipped false so
    // the local-form tests above keep working; the hub hot-reloads the flip
    // within ~15s), signalled here via a second env gate.
    test.skip(
      !process.env.OIDC_FORCE_SSO,
      "OIDC_FORCE_SSO not set — oidc-e2e.yaml ships forceSSO: false",
    );
    await page.goto("/login");
    await expect(
      page.getByRole("link", { name: "Sign in with SSO" }),
    ).toBeVisible();
    await expect(page.getByLabel("Password")).toHaveCount(0);
  });
});

// Demo mode — OPT-IN: the default `make e2e-ui` stack doesn't enable demo mode,
// so gate this like the OIDC tests. Needs AVURUOBS_DEMO_ENABLED=true and a
// `demo` project with data in the stack, signalled via DEMO_E2E=1.
test.describe("auth: demo", () => {
  test.skip(!process.env.DEMO_E2E, "DEMO_E2E not set — demo mode not enabled in the stack");

  test("'Try the demo' signs in as a read-only viewer scoped to demo", async ({ page }) => {
    await page.goto("/login");
    await page.getByRole("button", { name: /Try the demo/ }).click();
    // Lands in the app with a session (Sign out present), on the demo project.
    await expect(page).not.toHaveURL(/\/login/);
    await expect(page.getByRole("button", { name: "Sign out" })).toBeVisible();
    await expect(page.getByRole("button", { name: "Switch project" })).toContainText("demo");
    // Read-only: none of the administration tabs are offered. Users was
    // always gated; Groups, Storage and Status were not, so the shared demo
    // account was handed a group editor it could not save and two tabs whose
    // only endpoint refuses it. settings-viewer.spec.ts proves the rule
    // against a stubbed viewer on every run; this proves it against the real
    // demo account, which is where it was noticed.
    await page.goto("/settings");
    for (const name of ["Users", "Groups", "Storage", "Status"]) {
      await expect(page.getByRole("tab", { name })).toHaveCount(0);
    }
  });

  // The demo account is an ordinary LOCAL user, so the Account tab used to
  // offer it the change-password form on the strength of origin alone — and
  // the hub answered 403 on submit, after the visitor had typed a password.
  // The tab now reads the hub's passwordChange instead.
  test("the demo account is told why it cannot change its password", async ({ page }) => {
    await page.goto("/login");
    await page.getByRole("button", { name: /Try the demo/ }).click();
    await expect(page).not.toHaveURL(/\/login/);

    await page.goto("/settings?tab=account");
    // Signed in, so the Account tab is offered and the identity renders.
    await expect(page.getByTestId("account-origin")).toContainText("Password");
    // …but the form is replaced by the reason, not shown and then refused.
    await expect(page.getByTestId("account-shared-note")).toContainText("shared read-only demo");
    await expect(page.getByRole("button", { name: "Change password" })).toHaveCount(0);
    await expect(page.getByLabel("Current password")).toHaveCount(0);
  });
});
