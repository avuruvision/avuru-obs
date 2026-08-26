import { test, expect, type Page } from "@playwright/test";

// Service health groups authored from the UI. Self-contained via route
// interception — stubs /api/v1/auth/me and /api/v1/service-groups (GET plus
// POST/PUT/DELETE), so these run whatever auth mode the stack is in, matching
// the style of projects-admin.spec.ts.

type Group = {
  name: string;
  tier: string;
  namespaces: string[];
  services: string[];
  source: "config" | "db";
  editable: boolean;
  shadowed?: boolean;
};

async function stubIdentity(page: Page, role: "admin" | "viewer") {
  await page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({
      json: {
        user: { id: role, email: role, name: role, anonymous: false, origin: "local", passwordChange: "self" },
        grants: [{ scope: "*", role }],
      },
    }),
  );
}

// stubGroups serves a mutable in-memory list so a create/edit/delete
// round-trips through the UI exactly as it would against the hub.
async function stubGroups(page: Page, initial: Group[]) {
  const groups = [...initial];
  await page.route("**/api/v1/service-groups", async (route) => {
    if (route.request().method() === "POST") {
      const body = route.request().postDataJSON() as Omit<Group, "source" | "editable">;
      const created: Group = { ...body, source: "db", editable: true };
      groups.push(created);
      return route.fulfill({ status: 201, json: created });
    }
    return route.fulfill({ json: { groups } });
  });
  await page.route("**/api/v1/service-groups/*", async (route) => {
    const name = decodeURIComponent(route.request().url().split("/").pop() as string);
    const i = groups.findIndex((g) => g.name === name);
    if (route.request().method() === "PUT") {
      const body = route.request().postDataJSON() as Omit<Group, "name" | "source" | "editable">;
      if (i >= 0) groups[i] = { ...groups[i], ...body };
      return route.fulfill({ status: 200, json: groups[i] });
    }
    if (route.request().method() === "DELETE") {
      if (i >= 0) groups.splice(i, 1);
      return route.fulfill({ status: 204, body: "" });
    }
    return route.fallback();
  });
}

const CONFIG_GROUP: Group = {
  name: "core",
  tier: "T0",
  namespaces: ["core"],
  services: [],
  source: "config",
  editable: false,
};

const DB_GROUP: Group = {
  name: "payments",
  tier: "T1",
  namespaces: ["pay"],
  services: [],
  source: "db",
  editable: true,
};

test.describe("service groups", () => {
  test("an admin creates a group without touching values.yaml", async ({ page }) => {
    await stubIdentity(page, "admin");
    await stubGroups(page, []);

    await page.goto("/settings?tab=groups");
    await expect(page.getByTestId("service-groups-empty")).toBeVisible();

    await page.getByTestId("add-service-group").click();
    await page.getByTestId("service-group-name").fill("payments");
    await page.getByTestId("service-group-namespaces").fill("pay\nbilling");
    await page.getByTestId("service-group-save").click();

    const list = page.getByTestId("service-groups-list");
    await expect(list.getByText("payments", { exact: true })).toBeVisible();
    await expect(list.getByText(/namespaces: pay, billing/)).toBeVisible();
  });

  test("a chart-declared group is read-only", async ({ page }) => {
    await stubIdentity(page, "admin");
    await stubGroups(page, [CONFIG_GROUP, DB_GROUP]);

    await page.goto("/settings?tab=groups");
    // The config group offers no edit or delete — the hub lets the config win
    // a name collision, so an edit here would never take effect.
    await expect(page.getByTestId("edit-service-group-core")).toHaveCount(0);
    await expect(page.getByTestId("delete-service-group-core")).toHaveCount(0);
    await expect(page.getByText(/declared in the chart/)).toBeVisible();
    // The authored one is editable.
    await expect(page.getByTestId("edit-service-group-payments")).toBeVisible();
  });

  test("an admin edits a group's tier", async ({ page }) => {
    await stubIdentity(page, "admin");
    await stubGroups(page, [DB_GROUP]);

    await page.goto("/settings?tab=groups");
    await page.getByTestId("edit-service-group-payments").click();
    await page.getByRole("button", { name: "Criticality tier" }).click();
    await page.getByRole("listbox", { name: "Criticality tier" }).getByRole("option", { name: /^T0/ }).click();
    await page.getByTestId("service-group-save").click();

    await expect(page.getByTestId("service-groups-list").getByText("T0", { exact: true })).toBeVisible();
  });

  test("an admin deletes a group", async ({ page }) => {
    await stubIdentity(page, "admin");
    await stubGroups(page, [DB_GROUP]);

    await page.goto("/settings?tab=groups");
    await page.getByTestId("delete-service-group-payments").click();
    await expect(page.getByTestId("service-groups-empty")).toBeVisible();
  });

  // A group matching nothing is a silent no-op, so the form refuses it before
  // the request rather than storing something inert.
  test("an empty selector is refused with an explanation", async ({ page }) => {
    await stubIdentity(page, "admin");
    await stubGroups(page, []);

    await page.goto("/settings?tab=groups");
    await page.getByTestId("add-service-group").click();
    await page.getByTestId("service-group-name").fill("empty");
    await page.getByTestId("service-group-save").click();

    await expect(page.getByTestId("service-group-error")).toContainText(/at least one namespace or service/);
    await expect(page.getByTestId("service-group-form")).toBeVisible();
  });

  // Reads used to follow the health authorization while writes were
  // admin-only, so a viewer was shown a panel with every control hidden — a
  // group editor defined by what it would not let them do. Groups is
  // administration now, like Users: not offered at all. settings-viewer.spec.ts
  // owns the rule across the whole screen; this holds the editor's own half.
  test("a viewer is not offered the group editor", async ({ page }) => {
    await stubIdentity(page, "viewer");
    await stubGroups(page, [DB_GROUP]);

    await page.goto("/settings?tab=groups");
    await expect(page.getByRole("tab", { name: "Groups" })).toHaveCount(0);
    await expect(page.getByTestId("service-groups-list")).toHaveCount(0);
    await expect(page.getByTestId("add-service-group")).toHaveCount(0);
  });

  // The lanes are only as useful as the grouping behind them, so the board
  // that shows the grouping is one click from the screen that changes it.
  test("the health board links to the group editor", async ({ page }) => {
    await stubIdentity(page, "admin");
    await stubGroups(page, [DB_GROUP]);
    await page.route("**/api/v1/health/groups*", (route) =>
      route.fulfill({
        json: {
          overall: "healthy",
          checkedAt: new Date().toISOString(),
          window: { start: new Date().toISOString(), end: new Date().toISOString() },
          groups: [
            {
              name: "payments",
              tier: "T1",
              source: "config",
              status: "healthy",
              reason: "",
              counts: { healthy: 1 },
              spanCount: 10,
              ratePerSec: 1,
              errorRate: 0,
              p95Ms: 12,
              members: [],
            },
          ],
        },
      }),
    );

    await page.goto("/health");
    await page.getByTestId("manage-groups-link").click();
    await expect(page).toHaveURL(/\/settings\?tab=groups/);
    await expect(page.getByTestId("service-groups-list")).toBeVisible();
  });

  // With no telemetry yet, the empty state has to point at the editor too —
  // it used to tell people to go and edit a chart value.
  test("the empty health board points at the group editor", async ({ page }) => {
    await stubIdentity(page, "admin");
    await stubGroups(page, []);
    await page.route("**/api/v1/health/groups*", (route) =>
      route.fulfill({
        json: {
          overall: "healthy",
          checkedAt: new Date().toISOString(),
          window: { start: new Date().toISOString(), end: new Date().toISOString() },
          groups: [],
        },
      }),
    );

    await page.goto("/health");
    await page.getByRole("link", { name: /Settings → Groups/ }).click();
    await expect(page).toHaveURL(/\/settings\?tab=groups/);
    await expect(page.getByTestId("service-groups-empty")).toBeVisible();
  });
});
