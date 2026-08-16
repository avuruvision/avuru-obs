import { test, expect, type Page } from "@playwright/test";

// The OIDC group→role mapping panel (Settings → Access, SSO installs only).
// Self-contained via route interception like service-groups.spec.ts: stubs
// /api/v1/auth/me, /auth/config, /auth/permissions and the four
// /auth/oidc/mapping* routes, so these run whatever auth mode the stack is in.

type Rule = {
  group: string;
  role?: string;
  projects?: string[];
  source: "config" | "db";
  editable: boolean;
  shadowed?: boolean;
  invalid?: boolean;
  invalidRole?: string;
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

// The Access tab gates the panel on this endpoint reporting "oidc" — the hub
// only registers the mapping routes when an IdP is actually configured.
async function stubAuthConfig(page: Page, methods: ("local" | "oidc")[]) {
  await page.route("**/api/v1/auth/config", (route) =>
    route.fulfill({ json: { enabled: true, methods, forceSSO: false } }),
  );
}

// The permission matrix renders above the panel; a minimal answer keeps the
// tab from sitting on its spinner.
async function stubPermissions(page: Page) {
  await page.route("**/api/v1/auth/permissions", (route) =>
    route.fulfill({
      json: {
        authEnabled: true,
        roles: [
          { role: "admin", label: "Admin", description: "Everything." },
          { role: "viewer", label: "Viewer", description: "Reads." },
        ],
        areas: [{ area: "traces", label: "Traces", read: "viewer" }],
      },
    }),
  );
}

// stubMapping serves a mutable in-memory rule list so a create/edit/delete/
// reset round-trips through the UI exactly as it would against the hub.
// Returns a probe for the reset endpoint so a test can assert the confirm
// step really gates the request.
async function stubMapping(page: Page, initial: Rule[]) {
  let rules = [...initial];
  const fired = { reset: 0 };
  await page.route("**/api/v1/auth/oidc/mapping", (route) =>
    route.fulfill({ json: { rules } }),
  );
  await page.route("**/api/v1/auth/oidc/mapping/*", async (route) => {
    const tail = decodeURIComponent(route.request().url().split("/").pop() as string);
    if (route.request().method() === "POST" && tail === "reset") {
      fired.reset++;
      rules = rules.filter((r) => r.source === "config");
      return route.fulfill({ status: 204, body: "" });
    }
    const i = rules.findIndex((r) => r.source === "db" && r.group === tail);
    if (route.request().method() === "PUT") {
      const body = route.request().postDataJSON() as { role: string; projects: string[] };
      const saved: Rule = { group: tail, ...body, source: "db", editable: true };
      if (i >= 0) rules[i] = saved;
      else rules.push(saved);
      return route.fulfill({ status: 200, json: saved });
    }
    if (route.request().method() === "DELETE") {
      if (i >= 0) rules.splice(i, 1);
      return route.fulfill({ status: 204, body: "" });
    }
    return route.fallback();
  });
  return fired;
}

const CONFIG_RULE: Rule = {
  group: "sre",
  role: "admin",
  projects: ["*"],
  source: "config",
  editable: false,
};

const DB_RULE: Rule = {
  group: "platform-oncall",
  role: "editor",
  projects: ["default"],
  source: "db",
  editable: true,
};

test.describe("oidc group mapping", () => {
  test("an admin adds a rule without touching values.yaml", async ({ page }) => {
    await stubAdmin(page);
    await stubAuthConfig(page, ["local", "oidc"]);
    await stubPermissions(page);
    await stubMapping(page, []);

    await page.goto("/settings?tab=access");
    await expect(page.getByTestId("oidc-mapping-empty")).toBeVisible();

    await page.getByTestId("add-oidc-mapping").click();
    await page.getByTestId("oidc-mapping-group").fill("platform-oncall");
    await page.getByTestId("oidc-mapping-projects").fill("*");
    await page.getByTestId("oidc-mapping-save").click();

    const list = page.getByTestId("oidc-mapping-list");
    await expect(list.getByText("platform-oncall", { exact: true })).toBeVisible();
    await expect(list.getByText(/projects: \*/)).toBeVisible();
  });

  test("a chart-declared rule is read-only", async ({ page }) => {
    await stubAdmin(page);
    await stubAuthConfig(page, ["local", "oidc"]);
    await stubPermissions(page);
    await stubMapping(page, [CONFIG_RULE, DB_RULE]);

    await page.goto("/settings?tab=access");
    // The config rule offers no edit or delete — the hub lets the config win,
    // so an edit here would never take effect.
    await expect(page.getByTestId("oidc-mapping-list")).toBeVisible();
    await expect(page.getByTestId("edit-oidc-mapping-sre")).toHaveCount(0);
    await expect(page.getByTestId("delete-oidc-mapping-sre")).toHaveCount(0);
    await expect(page.getByText(/declared in the chart/)).toBeVisible();
    // The authored one stays editable.
    await expect(page.getByTestId("edit-oidc-mapping-platform-oncall")).toBeVisible();
  });

  // The collision the merge resolves silently in the hub must not be silent
  // here: an authored rule the chart also declares is stored but grants
  // nothing, and its row has to say so — and still offer Delete (d7ef9a0).
  test("a shadowed rule says why it stopped applying", async ({ page }) => {
    await stubAdmin(page);
    await stubAuthConfig(page, ["local", "oidc"]);
    await stubPermissions(page);
    await stubMapping(page, [
      CONFIG_RULE,
      { group: "sre", role: "viewer", projects: ["default"], source: "db", editable: true, shadowed: true },
    ]);

    await page.goto("/settings?tab=access");
    await expect(page.getByText("overridden by config")).toBeVisible();
    await expect(page.getByText(/config wins — this rule\s+stopped applying/)).toBeVisible();
    await expect(page.getByTestId("delete-oidc-mapping-sre")).toBeVisible();
  });

  test("reset is confirmed before firing", async ({ page }) => {
    await stubAdmin(page);
    await stubAuthConfig(page, ["local", "oidc"]);
    await stubPermissions(page);
    const fired = await stubMapping(page, [CONFIG_RULE, DB_RULE]);

    await page.goto("/settings?tab=access");
    const reset = page.getByTestId("reset-oidc-mapping");
    await reset.click();
    // First click arms; nothing has been sent.
    await expect(reset).toHaveText("Confirm reset");
    expect(fired.reset).toBe(0);

    await reset.click();
    // The authored rule is gone, the chart's own survives.
    await expect(page.getByTestId("oidc-mapping-list").getByText("platform-oncall")).toHaveCount(0);
    await expect(page.getByTestId("oidc-mapping-list").getByText("sre", { exact: true })).toBeVisible();
    expect(fired.reset).toBe(1);
  });

  // A rule with no projects grants nothing, so the form refuses it before the
  // request rather than storing something inert.
  test("an empty project list is refused with an explanation", async ({ page }) => {
    await stubAdmin(page);
    await stubAuthConfig(page, ["local", "oidc"]);
    await stubPermissions(page);
    await stubMapping(page, []);

    await page.goto("/settings?tab=access");
    await page.getByTestId("add-oidc-mapping").click();
    await page.getByTestId("oidc-mapping-group").fill("ghosts");
    await page.getByTestId("oidc-mapping-save").click();

    await expect(page.getByTestId("oidc-mapping-form-error")).toContainText(/at least one project/);
    await expect(page.getByTestId("oidc-mapping-form")).toBeVisible();
  });

  // Without an IdP the hub never registers the mapping routes, so mounting
  // the panel would only render 404s — the tab has to leave it out entirely.
  test("the panel is absent when SSO is not configured", async ({ page }) => {
    await stubAdmin(page);
    await stubAuthConfig(page, ["local"]);
    await stubPermissions(page);
    await stubMapping(page, [DB_RULE]);

    await page.goto("/settings?tab=access");
    await expect(page.getByTestId("permission-matrix")).toBeVisible();
    await expect(page.getByText("OIDC group mapping")).toHaveCount(0);
  });
});
