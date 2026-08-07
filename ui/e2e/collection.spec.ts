import { test, expect, type Page } from "@playwright/test";

// Settings → Collection runtime control (design/2026-07-27-collection-control-
// plane.md). Self-contained via route interception — stubs /api/v1/auth/me,
// /api/v1/capabilities, /api/v1/agents and the overlay endpoints, matching the
// route-stub style in ingest-keys.spec.ts, so these run without a cluster.
//
// The properties under test: the switches mirror the hub's EFFECTIVE state
// (not the raw overlay), edits batch into one PUT (each apply is one sensor
// rollout), reset is an explicit confirmed DELETE, and the writable card only
// exists when the capability AND the admin grant are both present.

type Overlay = {
  obiEnabled?: boolean;
  logsEnabled?: boolean;
  kubeletstatsEnabled?: boolean;
  profilerEnabled?: boolean;
  greenEnabled?: boolean;
  excludeNamespaces?: string[];
};

// The stub install's base values: profiling and green are off at install
// time (their modules are absent from capabilities below), everything else
// collects, kube-system excluded — mirrors a realistic default install.
const BASE = {
  obi: true,
  logs: true,
  kubeletstats: true,
  profiler: false,
  green: false,
  excludeNamespaces: ["kube-system"],
};

function effectiveOf(ov: Overlay) {
  return {
    obi: ov.obiEnabled ?? BASE.obi,
    logs: ov.logsEnabled ?? BASE.logs,
    kubeletstats: ov.kubeletstatsEnabled ?? BASE.kubeletstats,
    // One-way module gates: the modules are off at install, so the overlay
    // cannot widen them back on — same rule as the hub's EffectiveFromValues.
    profiler: false,
    green: false,
    excludeNamespaces: ov.excludeNamespaces ?? BASE.excludeNamespaces,
  };
}

async function stubIdentity(page: Page, role: "admin" | "viewer") {
  await page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({
      json: {
        user: { id: "u1", email: "admin@example.com", name: "Admin", anonymous: false },
        grants: [{ scope: "*", role }],
      },
    }),
  );
}

async function stubCapabilities(page: Page, runtimeControl: boolean) {
  await page.route("**/api/v1/capabilities", (route) =>
    route.fulfill({
      json: {
        version: "test",
        modules: ["core", "logs", "infra-metrics"],
        collectionRuntimeControl: runtimeControl,
      },
    }),
  );
}

async function stubAgents(page: Page) {
  await page.route("**/api/v1/agents**", (route) =>
    route.fulfill({ json: { sensors: [], windowSeconds: 600 } }),
  );
}

// stubOverlay serves a mutable overlay: PUT replaces it, DELETE resets it,
// GET reports it plus the effective state resolved against BASE — the same
// contract as the hub with an in-cluster applier. withEffective=false models
// a hub whose cluster read fails (the GET omits the key entirely).
async function stubOverlay(
  page: Page,
  initial: Overlay,
  opts?: { withEffective?: boolean },
) {
  const state = { overlay: initial, puts: [] as Overlay[], deletes: 0 };
  const withEffective = opts?.withEffective ?? true;
  await page.route("**/api/v1/collection/overlay", async (route) => {
    const method = route.request().method();
    if (method === "PUT") {
      const body = route.request().postDataJSON() as Overlay;
      state.overlay = body;
      state.puts.push(body);
      return route.fulfill({
        json: { overlay: body, updatedBy: "admin@example.com" },
      });
    }
    if (method === "DELETE") {
      state.overlay = {};
      state.deletes++;
      return route.fulfill({ json: { overlay: {} } });
    }
    return route.fulfill({
      json: {
        overlay: state.overlay,
        ...(withEffective ? { effective: effectiveOf(state.overlay) } : {}),
        updatedBy: "admin@example.com",
        updatedAt: "2026-08-04T10:00:00Z",
      },
    });
  });
  return state;
}

test.describe("collection runtime control", () => {
  test("capability off keeps the read-only Helm guidance", async ({ page }) => {
    await stubIdentity(page, "admin");
    await stubCapabilities(page, false);
    await stubAgents(page);

    await page.goto("/settings?tab=collection");
    await expect(page.getByRole("heading", { name: "Deactivating collection" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Collection control" })).toHaveCount(0);
    // The Helm knobs stay documented, including how to opt into the control plane.
    await expect(page.getByText("collection.runtimeControl.enabled=true")).toBeVisible();
    await expect(page.getByText("sensor.collection.excludeNamespaces")).toBeVisible();
  });

  test("a viewer never sees the writable card even with the capability on", async ({ page }) => {
    await stubIdentity(page, "viewer");
    await stubCapabilities(page, true);
    await stubAgents(page);

    await page.goto("/settings?tab=collection");
    await expect(page.getByRole("heading", { name: "Deactivating collection" })).toBeVisible();
    await expect(page.getByRole("heading", { name: "Collection control" })).toHaveCount(0);
  });

  test("switches mirror the effective state and batch into one PUT", async ({ page }) => {
    await stubIdentity(page, "admin");
    await stubCapabilities(page, true);
    await stubAgents(page);
    const state = await stubOverlay(page, {});

    await page.goto("/settings?tab=collection");
    await expect(page.getByRole("heading", { name: "Collection control" })).toBeVisible();

    // Effective state, not overlay state: the empty overlay shows BASE.
    await expect(page.getByRole("checkbox", { name: "Logs" })).toBeChecked();
    await expect(page.getByRole("checkbox", { name: "Traces (OBI)" })).toBeChecked();
    // Modules off at install render disabled with the enabling flag named —
    // the one-way rule made visible.
    await expect(page.getByRole("checkbox", { name: "Profiles" })).toBeDisabled();
    await expect(page.getByText("modules.profiling.enabled=true")).toBeVisible();

    // Two edits, still zero requests — Apply is the only writer.
    await page.getByRole("checkbox", { name: "Logs" }).click();
    await page.getByRole("checkbox", { name: "Pod metrics" }).click();
    expect(state.puts).toHaveLength(0);

    await page.getByRole("button", { name: "Apply changes" }).click();
    await expect.poll(() => state.puts.length).toBe(1);
    expect(state.puts).toEqual([{ logsEnabled: false, kubeletstatsEnabled: false }]);
    await expect(page.getByRole("checkbox", { name: "Logs" })).not.toBeChecked();

    // The refreshed GET marks the overridden rows.
    await expect(page.getByText("overridden")).toHaveCount(2);
  });

  test("namespace excludes validate inline and round-trip", async ({ page }) => {
    await stubIdentity(page, "admin");
    await stubCapabilities(page, true);
    await stubAgents(page);
    const state = await stubOverlay(page, {});

    await page.goto("/settings?tab=collection");
    await expect(page.getByText("kube-system")).toBeVisible();

    const input = page.getByRole("textbox", { name: "Namespace to exclude" });
    // An invalid name errors inline and never reaches the hub.
    await input.fill("Not A Namespace");
    await page.getByRole("button", { name: "Exclude" }).click();
    await expect(page.getByText('"Not A Namespace" is not a valid namespace name')).toBeVisible();

    await input.fill("payments");
    await input.press("Enter");
    await expect(page.getByText("payments")).toBeVisible();
    await page.getByRole("button", { name: "Apply changes" }).click();
    await expect.poll(() => state.puts.length).toBe(1);
    expect(state.puts).toEqual([{ excludeNamespaces: ["kube-system", "payments"] }]);
  });

  test("reset requires a confirm click and DELETEs back to chart defaults", async ({ page }) => {
    await stubIdentity(page, "admin");
    await stubCapabilities(page, true);
    await stubAgents(page);
    const state = await stubOverlay(page, { logsEnabled: false });

    await page.goto("/settings?tab=collection");
    await expect(page.getByRole("checkbox", { name: "Logs" })).not.toBeChecked();

    await page.getByRole("button", { name: "Reset to chart defaults" }).click();
    expect(state.deletes).toBe(0);
    await page.getByRole("button", { name: "Confirm reset" }).click();
    await expect.poll(() => state.deletes).toBe(1);

    // Back to BASE: logs collect again, the override badge is gone.
    await expect(page.getByRole("checkbox", { name: "Logs" })).toBeChecked();
    await expect(page.getByText("overridden")).toHaveCount(0);
  });

  test("a missing effective state disables editing rather than lying", async ({ page }) => {
    await stubIdentity(page, "admin");
    await stubCapabilities(page, true);
    await stubAgents(page);
    await stubOverlay(page, {}, { withEffective: false });

    await page.goto("/settings?tab=collection");
    await expect(
      page.getByText("couldn’t resolve the live collection state"),
    ).toBeVisible();
    await expect(page.getByRole("checkbox", { name: "Logs" })).toBeDisabled();
    await expect(page.getByRole("button", { name: "Apply changes" })).toBeDisabled();
  });
});
