import { test, expect, type Page } from "@playwright/test";

// Admin ingest-key management (auth Plan C). Self-contained via route
// interception — stubs /api/v1/auth/me (admin identity), /api/v1/projects and
// the per-project key endpoints, so these run without hub write support,
// matching the route-stub style in projects-admin.spec.ts.
//
// The property under test is the one-time secret: the raw key exists in exactly
// one response (create) and must never reappear from the list.

type Key = {
  keyHash: string;
  prefix: string;
  name: string;
  createdBy: string;
  createdAt: string;
};

const RAW_KEY = "avuruk_Zm9vYmFyYmF6cXV4MTIzNDU2Nzg";

async function stubAdmin(page: Page) {
  await page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({
      json: {
        user: { id: "admin", email: "admin", name: "Admin", anonymous: false },
        grants: [{ scope: "*", role: "admin" }],
      },
    }),
  );
  await page.route("**/api/v1/projects", (route) =>
    route.fulfill({
      json: {
        projects: [
          { id: "default", source: "default" },
          { id: "payments", label: "Payments", source: "db", editable: true },
        ],
      },
    }),
  );
}

// stubKeys serves a mutable in-memory key list. POST mints a key whose raw
// secret is returned ONCE; the list only ever carries prefix + metadata, so a
// leak in the list response fails the assertions below.
async function stubKeys(page: Page, initial: Key[]) {
  const keys = [...initial];

  // Registered before the collection route: Playwright matches routes in
  // reverse registration order, and "*" never spans "/", so the DELETE pattern
  // must not be shadowed by the collection pattern.
  await page.route("**/api/v1/projects/*/keys/*", async (route) => {
    if (route.request().method() === "DELETE") {
      const hash = decodeURIComponent(route.request().url().split("/").pop() as string);
      const i = keys.findIndex((k) => k.keyHash === hash);
      if (i >= 0) keys.splice(i, 1);
      return route.fulfill({ status: 204, body: "" });
    }
    return route.fallback();
  });

  await page.route("**/api/v1/projects/*/keys", async (route) => {
    if (route.request().method() === "POST") {
      const body = route.request().postDataJSON() as { name: string };
      const created: Key = {
        keyHash: "hash-" + body.name,
        prefix: RAW_KEY.slice(0, 12),
        name: body.name,
        createdBy: "admin",
        createdAt: new Date("2026-07-30T10:00:00Z").toISOString(),
      };
      keys.push(created);
      return route.fulfill({
        status: 201,
        json: { key: RAW_KEY, project: "payments", ...created },
      });
    }
    return route.fulfill({ json: { keys } });
  });
}

test.describe("admin ingest keys", () => {
  test("create reveals the raw key once and the list never leaks it", async ({ page }) => {
    await stubAdmin(page);
    await stubKeys(page, []);

    await page.goto("/settings?tab=general&project=payments");
    await expect(page.getByText("No keys yet.", { exact: false })).toBeVisible();

    await page.getByPlaceholder("prod-exporter").fill("prod-exporter");
    await page.getByRole("button", { name: "Create key" }).click();

    // The one and only time the raw secret is shown.
    const secret = page.getByTestId("ingest-key-secret");
    await expect(secret).toBeVisible();
    await expect(secret).toHaveText(RAW_KEY);

    // Reload: the key is listed by prefix, but the secret is unrecoverable.
    await page.reload();
    await expect(page.getByText("prod-exporter", { exact: false }).first()).toBeVisible();
    await expect(page.getByTestId("ingest-key-secret")).toHaveCount(0);
    await expect(page.getByText(RAW_KEY)).toHaveCount(0);
  });

  test("revoke removes the key from the list", async ({ page }) => {
    await stubAdmin(page);
    await stubKeys(page, [
      {
        keyHash: "hash-legacy",
        prefix: "avuruk_ab12",
        name: "legacy-exporter",
        createdBy: "admin",
        createdAt: new Date("2026-07-29T09:00:00Z").toISOString(),
      },
    ]);

    await page.goto("/settings?tab=general&project=payments");
    await expect(page.getByText("legacy-exporter")).toBeVisible();

    // Revoke is two-step: the first click arms the confirmation.
    await page.getByRole("button", { name: "Revoke" }).click();
    await page.getByRole("button", { name: "Revoke" }).click();

    await expect(page.getByText("legacy-exporter")).toHaveCount(0);
    await expect(page.getByText("No keys yet.", { exact: false })).toBeVisible();
  });

  test("a non-admin never sees the ingest-key card", async ({ page }) => {
    await page.route("**/api/v1/auth/me", (route) =>
      route.fulfill({
        json: {
          user: { id: "viewer", email: "viewer", name: "Viewer", anonymous: false },
          grants: [{ scope: "payments", role: "viewer" }],
        },
      }),
    );
    await page.route("**/api/v1/projects", (route) =>
      route.fulfill({
        json: { projects: [{ id: "payments", label: "Payments", source: "db" }] },
      }),
    );
    await stubKeys(page, []);

    await page.goto("/settings?tab=general&project=payments");
    await expect(page.getByText("Ingest API keys")).toHaveCount(0);
  });
});
