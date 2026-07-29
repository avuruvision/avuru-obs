import { test, expect, type Page } from "@playwright/test";

// Admin project CRUD (Phase 1). Self-contained via route interception — stubs
// /api/v1/auth/me (admin identity) and /api/v1/projects (GET + POST/PUT/DELETE),
// so these run without hub write support or a specific auth mode, matching the
// route-stub style in alerts.spec.ts. (The seeded-data switcher tests live in
// projects.spec.ts.)

type Proj = { id: string; label?: string; source: string; editable?: boolean };

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
      const body = route.request().postDataJSON() as { label: string };
      if (i >= 0) projects[i] = { ...projects[i], label: body.label };
      return route.fulfill({ status: 200, json: projects[i] });
    }
    if (route.request().method() === "DELETE") {
      if (i >= 0) projects.splice(i, 1);
      return route.fulfill({ status: 204, body: "" });
    }
    return route.fallback();
  });
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
});
