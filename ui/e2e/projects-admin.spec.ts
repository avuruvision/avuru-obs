import { test, expect, type Page } from "@playwright/test";

// Admin project CRUD (Phase 1). Self-contained via route interception — stubs
// /api/v1/auth/me (admin identity) and /api/v1/projects (GET + POST/PUT/DELETE),
// so these run without hub write support or a specific auth mode, matching the
// route-stub style in alerts.spec.ts. (The seeded-data switcher tests live in
// projects.spec.ts.)

type Proj = {
  id: string;
  label?: string;
  source: string;
  editable?: boolean;
  members?: string[];
  retentionDays?: number;
};

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

// stubProjects installs a mutable in-memory project list served by GET and
// mutated by POST/PUT/DELETE, so a create/rename/delete round-trips in the UI.
async function stubProjects(page: Page, initial: Proj[]) {
  const projects = [...initial];
  await page.route("**/api/v1/projects", async (route) => {
    if (route.request().method() === "POST") {
      const body = route.request().postDataJSON() as { id: string; label: string };
      const created: Proj = { id: body.id, label: body.label, source: "db", editable: true };
      projects.push(created);
      return route.fulfill({ status: 201, json: created });
    }
    return route.fulfill({ json: { projects } });
  });
  await page.route("**/api/v1/projects/*", async (route) => {
    const url = route.request().url();
    const id = decodeURIComponent(url.split("/").pop() as string);
    const i = projects.findIndex((p) => p.id === id);
    if (route.request().method() === "PUT") {
      // Mirrors the hub: an omitted field keeps its stored value.
      const body = route.request().postDataJSON() as {
        label?: string;
        members?: string[];
        retentionDays?: number;
      };
      if (i >= 0) {
        // Mirrors the hub's 409: an aggregate owns no data, so it cannot carry
        // a retention window of its own.
        const next = {
          ...projects[i],
          ...(body.label !== undefined ? { label: body.label } : {}),
          ...(body.members !== undefined ? { members: [...body.members].sort() } : {}),
          ...(body.retentionDays !== undefined ? { retentionDays: body.retentionDays } : {}),
        };
        if ((next.members?.length ?? 0) > 0 && (next.retentionDays ?? 0) > 0) {
          return route.fulfill({
            status: 409,
            json: {
              error: `project "${id}" is an aggregate of other projects; set retention on the member projects, which is where the data lives`,
            },
          });
        }
        projects[i] = next;
      }
      return route.fulfill({ status: 200, json: projects[i] });
    }
    if (route.request().method() === "DELETE") {
      if (i >= 0) projects.splice(i, 1);
      return route.fulfill({ status: 204, body: "" });
    }
    return route.fallback();
  });
}

// stubStatus serves the install-wide retention the Retention card reads; the
// per-project editor renders inside that card, so it needs both.
async function stubStatus(page: Page, retentionDays = 30) {
  await page.route("**/api/v1/system/status", (route) =>
    route.fulfill({
      json: {
        version: "test",
        overall: "healthy",
        checkedAt: new Date(0).toISOString(),
        components: [],
        signals: [
          { signal: "traces", rows: 0, bytes: 0, compressedBytes: 0, compression: 1, retentionDays, ttlDays: retentionDays },
        ],
        disks: [],
      },
    }),
  );
}

test.describe("admin project management", () => {
  test("admin creates a project and it appears in the switcher", async ({ page }) => {
    await stubAdmin(page);
    await stubProjects(page, [{ id: "default", source: "default" }]);

    await page.goto("/settings?tab=general");
    await page.getByRole("button", { name: "New project", exact: true }).click(); // reveal
    await page.getByLabel("Project id").fill("team-a");
    await page.getByLabel("Project name").fill("Team A");
    await page.getByRole("button", { name: "New project", exact: true }).click(); // submit

    await page.getByRole("button", { name: "Switch project" }).click();
    await expect(page.getByRole("option", { name: "Team A" })).toBeVisible();
  });

  test("a config project shows the read-only banner", async ({ page }) => {
    await stubAdmin(page);
    await stubProjects(page, [
      { id: "default", source: "default" },
      { id: "prod", source: "config" },
    ]);

    await page.goto("/settings?tab=general&project=prod");
    await expect(page.getByText("config-defined")).toBeVisible();
    await expect(
      page.getByText(/Projects are defined through deployment configuration/),
    ).toBeVisible();
    // A config project is not editable: no rename field, no delete.
    await expect(page.getByText("Danger zone")).toHaveCount(0);
  });

  test("admin deletes a db project", async ({ page }) => {
    await stubAdmin(page);
    await stubProjects(page, [
      { id: "default", source: "default" },
      { id: "team-a", label: "Team A", source: "db", editable: true },
    ]);

    await page.goto("/settings?tab=general&project=team-a");
    await expect(page.getByText("Danger zone")).toBeVisible();
    await page.getByRole("button", { name: "Delete project" }).click();

    // After delete the context falls back to default; team-a is gone from the switcher.
    await page.getByRole("button", { name: "Switch project" }).click();
    await expect(page.getByRole("option", { name: "Team A" })).toHaveCount(0);
  });

  test("admin turns a project into an aggregate and the switcher marks it", async ({ page }) => {
    await stubAdmin(page);
    await stubProjects(page, [
      { id: "default", source: "default" },
      { id: "estate", label: "Estate", source: "db", editable: true, members: [] },
      { id: "prod-eu", source: "data" },
      { id: "prod-us", source: "data" },
    ]);

    await page.goto("/settings?tab=general&project=estate");
    await expect(page.getByRole("heading", { name: "Member projects" })).toBeVisible();

    await page.getByLabel("Include prod-eu").check();
    await page.getByLabel("Include prod-us").check();
    await page.getByRole("button", { name: "Save members" }).click();
    await expect(page.getByText("2 selected")).toBeVisible();
    await expect(page.getByText("aggregate", { exact: true })).toBeVisible();

    // The switcher marks an aggregate so a cross-cluster view is never a
    // surprise ("why do I see services I do not recognize").
    await page.getByRole("button", { name: "Switch project" }).click();
    await expect(page.getByLabel("aggregate of 2 projects").first()).toBeVisible();
  });

  test("an aggregate cannot contain another aggregate", async ({ page }) => {
    await stubAdmin(page);
    await stubProjects(page, [
      { id: "default", source: "default" },
      { id: "estate", label: "Estate", source: "db", editable: true, members: [] },
      { id: "europe", label: "Europe", source: "db", editable: true, members: ["prod-eu"] },
    ]);

    await page.goto("/settings?tab=general&project=estate");
    // Aggregates are not offered as members at all — the hub also refuses with
    // a 409, but the UI never presents the dead end.
    await expect(page.getByLabel("Include europe")).toHaveCount(0);
  });

  test("a member id can be added before its cluster reports", async ({ page }) => {
    await stubAdmin(page);
    await stubProjects(page, [
      { id: "default", source: "default" },
      { id: "estate", label: "Estate", source: "db", editable: true, members: [] },
    ]);

    await page.goto("/settings?tab=general&project=estate");
    await page.getByLabel("Add a project id").fill("prod-ap");
    await page.getByRole("button", { name: "Add", exact: true }).click();
    await expect(page.getByText("(no data yet)")).toBeVisible();

    await page.getByRole("button", { name: "Save members" }).click();
    await expect(page.getByText("1 selected")).toBeVisible();
  });

  test("admin sets a shorter retention window on a project", async ({ page }) => {
    await stubAdmin(page);
    await stubStatus(page, 30);
    await stubProjects(page, [
      { id: "default", source: "default" },
      { id: "staging", label: "Staging", source: "db", editable: true, members: [] },
    ]);

    await page.goto("/settings?tab=general&project=staging");
    await page.getByLabel("Project retention in days").fill("3");
    await page.getByRole("button", { name: "Save retention" }).click();
    await expect(page.getByText(/Older telemetry is removed/)).toBeVisible();
  });

  // An aggregate reads its members and stores nothing, so there is no window to
  // set on it — the UI says where to set one instead of offering a dead end.
  test("an aggregate offers no retention field", async ({ page }) => {
    await stubAdmin(page);
    await stubStatus(page, 30);
    await stubProjects(page, [
      { id: "default", source: "default" },
      { id: "estate", label: "Estate", source: "db", editable: true, members: ["prod-eu"] },
    ]);

    await page.goto("/settings?tab=general&project=estate");
    await expect(page.getByLabel("Project retention in days")).toHaveCount(0);
    await expect(page.getByText(/An aggregate stores no telemetry of its own/)).toBeVisible();
  });
});
