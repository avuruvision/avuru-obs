import { test, expect } from "@playwright/test";

// Seeded fixture (deploy/compose/seed/fixtures): one deterministic trace.
const SEED_TRACE_ID = "aaaa1111bbbb2222cccc3333dddd4444";
const SEED_SERVICE = "seed-checkout";

test.describe("shell", () => {
  test("renders sidebar nav and toggles theme", async ({ page }) => {
    await page.goto("/traces");

    await expect(page.getByRole("link", { name: "avuru obs" })).toBeVisible();
    for (const item of ["Services", "Service Map", "Traces", "Logs", "Profiling"]) {
      await expect(page.getByRole("link", { name: item, exact: true })).toBeVisible();
    }

    // Dark is the default; the switch flips data-theme on <html>.
    const html = page.locator("html");
    await expect(html).toHaveAttribute("data-theme", "dark");
    await page.getByRole("button", { name: "Switch to light theme" }).click();
    await expect(html).toHaveAttribute("data-theme", "light");
    await page.getByRole("button", { name: "Switch to dark theme" }).click();
    await expect(html).toHaveAttribute("data-theme", "dark");
  });

  test("not-yet-built routes teach what arrives when", async ({ page }) => {
    await page.goto("/nodes");
    await expect(page.getByText("Node & pod health")).toBeVisible();
    await expect(page.getByText(/arrives in M\d/)).toBeVisible();
  });

  test("sidebar groups nav into sections", async ({ page }) => {
    await page.goto("/traces");
    // Scoped to the sidebar: the breadcrumb repeats the section name.
    const sidebar = page.getByRole("navigation", { name: "Primary" });
    for (const section of ["Observe", "Infrastructure", "System"]) {
      await expect(sidebar.getByText(section, { exact: true })).toBeVisible();
    }
    // Masthead shows a breadcrumb derived from the route.
    await expect(page.getByLabel("Breadcrumb")).toContainText("Traces");
  });

  test("service map renders the seeded service graph", async ({ page }) => {
    await page.goto("/service-map");
    // Seeded data → not the empty state; the screen summarises the nodes.
    await expect(page.getByText(/click a node for its traces/)).toBeVisible();
  });
});

test.describe("traces screen (seeded data)", () => {
  test("overview lists the seeded operation with its error rate", async ({ page }) => {
    await page.goto("/traces");

    const row = page.getByRole("row").filter({ hasText: SEED_SERVICE });
    await expect(row).toBeVisible();
    await expect(row).toContainText("POST /checkout");
    // The seeded root span is an error → the row carries an error badge.
    await expect(row.locator("text=%")).toBeVisible();
  });

  test("drill down: overview row → filtered list → waterfall → span detail", async ({ page }) => {
    await page.goto("/traces");

    // Row click filters to the operation and switches to the Traces tab.
    await page.getByRole("row").filter({ hasText: SEED_SERVICE }).click();
    await expect(page).toHaveURL(/tab=traces/);
    await expect(page).toHaveURL(new RegExp(`service=${SEED_SERVICE}`));

    // Exactly one deterministic trace; open it.
    const traceRow = page.getByRole("row").filter({ hasText: "POST /checkout" }).last();
    await traceRow.click();
    await expect(page).toHaveURL(new RegExp(`trace=${SEED_TRACE_ID}`));

    // Detail panel: id + span count + the three known spans in the waterfall.
    await expect(page.getByText(SEED_TRACE_ID)).toBeVisible();
    await expect(page.getByText("3 spans")).toBeVisible();
    for (const op of ["POST /checkout", "SELECT orders", "GET cache"]) {
      await expect(
        page.getByRole("button", { name: new RegExp(op.replace(/[/]/g, "\\/")) }),
      ).toBeVisible();
    }

    // Expand the failing SQL span: status message + exception event surface.
    await page.getByRole("button", { name: /SELECT orders/ }).click();
    await expect(page.getByText("connection refused").first()).toBeVisible();
    await expect(page.getByText("exception", { exact: true })).toBeVisible();
    await expect(page.getByText("ConnectionError")).toBeVisible();
  });

  test("?trace= URL is shareable: direct load opens the waterfall", async ({ page }) => {
    await page.goto(`/traces?trace=${SEED_TRACE_ID}&tab=traces`);

    await expect(page.getByText(SEED_TRACE_ID)).toBeVisible();
    await expect(page.getByText("3 spans")).toBeVisible();
    await expect(page.getByRole("button", { name: /GET cache/ })).toBeVisible();
  });

  test("heatmap renders cells and filters by duration band on click", async ({ page }) => {
    await page.goto("/traces");

    const grid = page.getByRole("grid", { name: "Latency heatmap" });
    await expect(grid).toBeVisible();
    const cells = grid.getByRole("gridcell");
    await expect.poll(async () => cells.count(), { timeout: 15_000 }).toBeGreaterThan(0);

    await cells.first().click();
    await expect(page).toHaveURL(/minMs=/);
    await expect(page).toHaveURL(/tab=traces/);
  });
});
