import { test, expect, type Page } from "@playwright/test";

// Users CRUD + self-service password change (design/2026-08-06-users-crud-password.md).
// Self-contained via route interception — stubs /api/v1/auth/me, /api/v1/projects
// and the users endpoints — so these run without hub write support, matching the
// route-stub style in ingest-keys.spec.ts.
//
// The properties under test are the guards, not the plumbing: delete is offered
// only on already-disabled rows, an SSO row offers no password reset and warns
// that deleting UNDOES the lockout, and a hub 400 lands in front of the admin
// instead of vanishing.

type Grant = { scope: string; role: "viewer" | "editor" | "admin" };
type User = {
  id: string;
  email: string;
  name: string;
  origin: string;
  disabled: boolean;
  grants: Grant[];
};

const ALICE: User = {
  id: "u-alice",
  email: "alice@example.com",
  name: "Alice",
  origin: "local",
  disabled: false,
  grants: [{ scope: "payments", role: "viewer" }],
};

const BOB_DISABLED: User = {
  id: "u-bob",
  email: "bob@example.com",
  name: "Bob",
  origin: "local",
  disabled: true,
  grants: [],
};

const SSO_DISABLED: User = {
  id: "u-sso",
  email: "carol@example.com",
  name: "Carol",
  origin: "oidc",
  disabled: true,
  grants: [{ scope: "*", role: "viewer" }],
};

// stubMe serves the signed-in identity. passwordChange drives the Account
// tab's fork (change-password form vs. a note saying why not), so it is a
// parameter, not a constant; origin only labels the "Sign-in" cell now.
//
// The default mirrors what the hub's passwordChangeFor would return for this
// identity — an ordinary local admin — so a stub can't accidentally describe a
// user the server could never produce. Pass them explicitly when the case
// under test needs them to disagree.
async function stubMe(
  page: Page,
  opts: {
    origin?: string;
    admin?: boolean;
    anonymous?: boolean;
    passwordChange?: string;
  } = {},
) {
  const {
    origin = "local",
    admin = true,
    anonymous = false,
    passwordChange = anonymous ? "" : origin === "local" ? "self" : "idp",
  } = opts;
  await page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({
      json: {
        user: {
          id: "admin",
          email: "admin@example.com",
          name: "Admin",
          origin,
          anonymous,
          passwordChange,
        },
        grants: admin ? [{ scope: "*", role: "admin" }] : [{ scope: "payments", role: "viewer" }],
      },
    }),
  );
  await page.route("**/api/v1/projects", (route) =>
    route.fulfill({ json: { projects: [{ id: "default", source: "default" }] } }),
  );
}

// The hub's error envelope (errorBody in hub/internal/api/errors.go). The SPA
// reads error.message and falls back to the HTTP statusText, so a stub with a
// flat {error: "..."} would silently pass the fallback through and the tests
// below would assert nothing about the hub's actual copy.
function errBody(code: number, message: string) {
  return { error: { code, message } };
}

type Recorded = { method: string; url: string; body: unknown };

// stubUsers serves a mutable in-memory user list and records every write, so a
// test can assert on the REQUEST the panel sent (PUT semantics: an absent field
// means "leave unchanged") and not just on what the stub echoed back.
async function stubUsers(page: Page, initial: User[]): Promise<Recorded[]> {
  const users = [...initial];
  const calls: Recorded[] = [];

  // Registered before the collection route: Playwright matches routes in reverse
  // registration order, and "*" never spans "/", so the per-user pattern must not
  // be shadowed by the collection pattern.
  await page.route("**/api/v1/users/*", async (route) => {
    const req = route.request();
    const id = decodeURIComponent(req.url().split("/").pop() as string);
    const i = users.findIndex((u) => u.id === id);

    if (req.method() === "DELETE") {
      calls.push({ method: "DELETE", url: req.url(), body: null });
      if (i >= 0) users.splice(i, 1);
      return route.fulfill({ status: 204, body: "" });
    }
    if (req.method() === "PUT") {
      const body = req.postDataJSON() as Partial<User> & { password?: string };
      calls.push({ method: "PUT", url: req.url(), body });
      if (i < 0) return route.fulfill({ status: 404, json: errBody(404, "not found") });
      // Mirror the hub: absent field = unchanged; grants replace wholesale.
      if (body.name !== undefined) users[i].name = body.name;
      if (body.disabled !== undefined) users[i].disabled = body.disabled;
      if (body.grants !== undefined) users[i].grants = body.grants;
      return route.fulfill({ json: users[i] });
    }
    return route.fallback();
  });

  await page.route("**/api/v1/users", async (route) => {
    if (route.request().method() === "POST") {
      const body = route.request().postDataJSON() as User;
      const created: User = {
        id: "u-" + body.email,
        email: body.email,
        name: body.name,
        origin: "local",
        disabled: false,
        grants: body.grants ?? [],
      };
      users.push(created);
      return route.fulfill({ status: 201, json: created });
    }
    return route.fulfill({ json: { users } });
  });

  return calls;
}

function row(page: Page, email: string) {
  return page.getByTestId(`user-row-${email}`);
}

function panel(page: Page, email: string) {
  return page.getByTestId(`user-panel-${email}`);
}

test.describe("admin users panel", () => {
  test("edit round-trips name and grants through PUT", async ({ page }) => {
    await stubMe(page);
    const calls = await stubUsers(page, [ALICE]);

    await page.goto("/settings?tab=users");
    await expect(row(page, ALICE.email)).toBeVisible();
    await expect(row(page, ALICE.email)).toContainText("viewer@payments");

    await row(page, ALICE.email).getByRole("button", { name: "Edit" }).click();
    const form = panel(page, ALICE.email);
    await form.getByPlaceholder("Jane Doe").fill("Alice Cooper");
    await form.getByLabel("Role").selectOption("editor");
    await form.getByRole("button", { name: "Save changes" }).click();

    // The grant set is sent whole (the hub replaces it), and the name rides
    // along in the same request.
    const put = calls.find((c) => c.method === "PUT");
    expect(put?.body).toMatchObject({
      name: "Alice Cooper",
      grants: [{ scope: "payments", role: "editor" }],
    });
    await expect(row(page, ALICE.email)).toContainText("Alice Cooper");
    await expect(row(page, ALICE.email)).toContainText("editor@payments");
  });

  test("a duplicate grant scope is caught before any request", async ({ page }) => {
    await stubMe(page);
    const calls = await stubUsers(page, [ALICE]);

    await page.goto("/settings?tab=users");
    await row(page, ALICE.email).getByRole("button", { name: "Edit" }).click();

    const form = panel(page, ALICE.email);
    await form.getByRole("button", { name: "Add grant" }).click();
    // Same scope twice — the hub would 400; the panel says so without asking.
    await form.getByLabel("Project scope").nth(1).fill("payments");

    await expect(form.getByText("Duplicate grant scope")).toBeVisible();
    await expect(form.getByRole("button", { name: "Save changes" })).toBeDisabled();
    expect(calls.filter((c) => c.method === "PUT")).toHaveLength(0);
  });

  test("reset password sends only the password field", async ({ page }) => {
    await stubMe(page);
    const calls = await stubUsers(page, [ALICE]);

    await page.goto("/settings?tab=users");
    await row(page, ALICE.email).getByRole("button", { name: "Reset password" }).click();

    const form = panel(page, ALICE.email);
    await expect(form.getByText("Signs this user out everywhere")).toBeVisible();
    await form.getByPlaceholder("••••••••").fill("s3cret-rotation");
    await form.getByRole("button", { name: "Set password" }).click();

    const put = calls.find((c) => c.method === "PUT");
    // Only the password — name/grants/disabled stay absent so the hub leaves
    // them untouched.
    expect(put?.body).toEqual({ password: "s3cret-rotation" });
  });

  test("an SSO user is offered no password reset", async ({ page }) => {
    await stubMe(page);
    await stubUsers(page, [SSO_DISABLED]);

    await page.goto("/settings?tab=users");
    await expect(row(page, SSO_DISABLED.email)).toBeVisible();
    await expect(
      row(page, SSO_DISABLED.email).getByRole("button", { name: "Reset password" }),
    ).toHaveCount(0);
    await expect(row(page, SSO_DISABLED.email)).toContainText("SSO");
  });

  test("delete is offered only once a user is disabled", async ({ page }) => {
    await stubMe(page);
    await stubUsers(page, [ALICE, BOB_DISABLED]);

    await page.goto("/settings?tab=users");
    // Active: the hub would answer 409, so the button isn't there at all.
    await expect(row(page, ALICE.email).getByRole("button", { name: "Delete" })).toHaveCount(0);
    // Disabled: the recovery window has been taken, so delete is offered.
    await expect(row(page, BOB_DISABLED.email).getByRole("button", { name: "Delete" })).toBeVisible();
  });

  test("deleting a local user takes two clicks and removes the row", async ({ page }) => {
    await stubMe(page);
    const calls = await stubUsers(page, [ALICE, BOB_DISABLED]);

    await page.goto("/settings?tab=users");
    await row(page, BOB_DISABLED.email).getByRole("button", { name: "Delete" }).click();

    // First click only arms the confirmation — nothing has been sent yet.
    await expect(page.getByTestId("delete-warning-local")).toBeVisible();
    await expect(page.getByTestId("delete-warning-local")).toContainText("cannot be recovered");
    expect(calls.filter((c) => c.method === "DELETE")).toHaveLength(0);

    await page.getByRole("button", { name: "Delete permanently" }).click();

    await expect(row(page, BOB_DISABLED.email)).toHaveCount(0);
    await expect(row(page, ALICE.email)).toBeVisible();
    expect(calls.filter((c) => c.method === "DELETE")).toHaveLength(1);
  });

  test("deleting a disabled SSO user warns that it undoes the lockout", async ({ page }) => {
    await stubMe(page);
    await stubUsers(page, [SSO_DISABLED]);

    await page.goto("/settings?tab=users");
    await row(page, SSO_DISABLED.email).getByRole("button", { name: "Delete" }).click();

    // The whole point of the SSO branch: `disabled` is what the SSO callback
    // checks, so deleting the local record lets them back in on the next IdP
    // login. An admin must not read this as "removes their access".
    const warning = page.getByTestId("delete-warning-sso");
    await expect(warning).toBeVisible();
    await expect(warning).toContainText("undoes this lockout");
    await expect(warning).toContainText("Keep them disabled");
    await expect(page.getByTestId("delete-warning-local")).toHaveCount(0);
  });

  test("a hub 400 (self-lockout) surfaces on the row", async ({ page }) => {
    await stubMe(page);
    await stubUsers(page, [ALICE]);
    // Registered last, so it wins over the stub above: the hub rejects an admin
    // disabling their own account.
    await page.route("**/api/v1/users/*", (route) =>
      route.fulfill({
        status: 400,
        json: errBody(400, "cannot disable or de-admin your own account"),
      }),
    );

    await page.goto("/settings?tab=users");
    await row(page, ALICE.email).getByRole("button", { name: "Disable" }).click();

    await expect(page.getByTestId("user-row-error")).toContainText(
      "cannot disable or de-admin your own account",
    );
    // The row is unchanged — a rejected write must not look applied.
    await expect(row(page, ALICE.email)).toContainText("active");
  });

  test("a non-admin gets no Users tab", async ({ page }) => {
    await stubMe(page, { admin: false });
    await stubUsers(page, [ALICE]);

    await page.goto("/settings?tab=users");
    await expect(page.getByRole("tab", { name: "Users" })).toHaveCount(0);
    await expect(row(page, ALICE.email)).toHaveCount(0);
  });
});

test.describe("settings account tab", () => {
  test("a local user changes their own password", async ({ page }) => {
    await stubMe(page, { origin: "local" });
    let sent: { currentPassword: string; newPassword: string } | null = null;
    await page.route("**/api/v1/auth/password", (route) => {
      sent = route.request().postDataJSON();
      return route.fulfill({
        json: {
          user: {
            id: "admin",
            email: "admin@example.com",
            name: "Admin",
            origin: "local",
            anonymous: false,
            // The hub answers this route with meFrom too, so the stub carries
            // the same field /auth/me does.
            passwordChange: "self",
          },
          grants: [{ scope: "*", role: "admin" }],
        },
      });
    });

    await page.goto("/settings?tab=account");
    await expect(page.getByTestId("account-origin")).toContainText("Password");

    await page.getByLabel("Current password").fill("old-password");
    await page.getByLabel("New password", { exact: true }).fill("new-password");
    await page.getByLabel("Confirm new password").fill("new-password");
    await page.getByRole("button", { name: "Change password" }).click();

    await expect(page.getByTestId("password-changed")).toBeVisible();
    // The hub re-mints the session on this response, so the user stays here.
    await expect(page.getByTestId("password-changed")).toContainText("stay signed in");
    expect(sent).toEqual({ currentPassword: "old-password", newPassword: "new-password" });
    // Fields are cleared so a stray second submit can't replay a stale current.
    await expect(page.getByLabel("Current password")).toHaveValue("");
  });

  test("a mismatched confirmation never reaches the hub", async ({ page }) => {
    await stubMe(page, { origin: "local" });
    let calls = 0;
    await page.route("**/api/v1/auth/password", (route) => {
      calls++;
      return route.fulfill({ status: 500, json: errBody(500, "should not be called") });
    });

    await page.goto("/settings?tab=account");
    await page.getByLabel("Current password").fill("old-password");
    await page.getByLabel("New password", { exact: true }).fill("new-password");
    await page.getByLabel("Confirm new password").fill("typo-password");

    await expect(page.getByTestId("password-mismatch")).toBeVisible();
    await expect(page.getByRole("button", { name: "Change password" })).toBeDisabled();
    expect(calls).toBe(0);
  });

  test("a wrong current password surfaces as a field error, not a sign-out", async ({ page }) => {
    await stubMe(page, { origin: "local" });
    await page.route("**/api/v1/auth/password", (route) =>
      route.fulfill({ status: 400, json: errBody(400, "current password is incorrect") }),
    );

    await page.goto("/settings?tab=account");
    await page.getByLabel("Current password").fill("wrong");
    await page.getByLabel("New password", { exact: true }).fill("new-password");
    await page.getByLabel("Confirm new password").fill("new-password");
    await page.getByRole("button", { name: "Change password" }).click();

    await expect(page.getByTestId("password-error")).toContainText("current password is incorrect");
    // 400 not 401 on purpose — the SPA bounces to /login on a 401, which would
    // eat the error mid-form.
    await expect(page).toHaveURL(/\/settings/);
  });

  test("an SSO user sees the IdP note instead of the form", async ({ page }) => {
    await stubMe(page, { origin: "oidc" });

    await page.goto("/settings?tab=account");
    await expect(page.getByTestId("account-origin")).toContainText("Single sign-on");
    await expect(page.getByTestId("account-idp-note")).toContainText(
      "managed by your identity provider",
    );
    await expect(page.getByLabel("Current password")).toHaveCount(0);
  });

  test("the anonymous fallback gets no Account tab", async ({ page }) => {
    await stubMe(page, { origin: "", anonymous: true, admin: false });

    await page.goto("/settings?tab=account");
    await expect(page.getByRole("tab", { name: "Account" })).toHaveCount(0);
    await expect(page.getByLabel("Current password")).toHaveCount(0);
  });
});
