import { test, expect, type Page } from "@playwright/test";

// Settings is administration. A read-only account — the shared demo viewer
// above all — must not be offered the tabs that configure the install: the
// group editor it cannot save, and the two tabs whose only endpoint
// (/api/v1/system/status) is admin-only and answered them with a "couldn't
// reach the hub" error that was really a refusal.
//
// The suite signs in as an admin (global-setup), so the viewer is made by
// stubbing /auth/me — the one call the gate reads. A global "*" viewer grant,
// not a project-scoped one, so the project self-heal in project-context stays
// out of the way: the subject here is the role, not the scope.
const VIEWER = {
  user: {
    id: "demo-viewer",
    email: "demo@avuru.obs",
    name: "Demo (read-only)",
    origin: "local",
    anonymous: false,
    passwordChange: "shared",
  },
  grants: [{ scope: "*", role: "viewer" }],
};

async function asViewer(page: Page) {
  await page.route("**/api/v1/auth/me", (route) => route.fulfill({ json: VIEWER }));
}

const ADMIN_TABS = ["Groups", "Storage", "Status"] as const;

test.describe("settings: administration is admin-only", () => {
  test("an admin still sees every configuration tab", async ({ page }) => {
    await page.goto("/settings");
    await expect(page.getByRole("tab", { name: "Users" })).toBeVisible();
    for (const name of ADMIN_TABS) {
      await expect(page.getByRole("tab", { name })).toBeVisible();
    }
  });

  test("a read-only viewer is offered none of them", async ({ page }) => {
    await asViewer(page);
    await page.goto("/settings");

    // What a viewer keeps: the project it is looking at, what is collecting,
    // why it is refused things, and its own account.
    await expect(page.getByRole("tab", { name: "General" })).toBeVisible();
    await expect(page.getByRole("tab", { name: "Collection" })).toBeVisible();
    await expect(page.getByRole("tab", { name: "Access" })).toBeVisible();
    await expect(page.getByRole("tab", { name: "Account" })).toBeVisible();

    for (const name of ADMIN_TABS) {
      await expect(page.getByRole("tab", { name })).toHaveCount(0);
    }
    await expect(page.getByRole("tab", { name: "Users" })).toHaveCount(0);
  });

  test("a viewer deep-linking straight to a hidden tab lands on General", async ({ page }) => {
    await asViewer(page);

    // The group editor: not rendered, not merely disabled.
    await page.goto("/settings?tab=groups");
    await expect(page.getByRole("heading", { name: "Service groups" })).toHaveCount(0);
    await expect(page.getByTestId("add-service-group")).toHaveCount(0);
    await expect(page.getByRole("tab", { name: "General" })).toHaveAttribute(
      "aria-selected",
      "true",
    );

    // And the storage tab, whose error message was an outage that was not
    // happening.
    await page.goto("/settings?tab=storage");
    await expect(page.getByText(/Couldn’t reach the hub/)).toHaveCount(0);
    await expect(page.getByRole("tab", { name: "General" })).toHaveAttribute(
      "aria-selected",
      "true",
    );
  });

  test("the health board stops pointing a viewer at the group editor", async ({ page }) => {
    // Admin: the door is there.
    await page.goto("/health");
    await expect(page.getByTestId("manage-groups-link")).toBeVisible();

    await asViewer(page);
    await page.goto("/health");
    // The board itself is unchanged — same lanes, same data, one fewer link.
    await expect(page.getByText(/Overall:/)).toBeVisible();
    await expect(page.getByTestId("manage-groups-link")).toHaveCount(0);
  });
});
