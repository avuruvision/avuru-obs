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

    // Detail panel: id + summary bar + the three known spans in the waterfall.
    await expect(page.getByText(SEED_TRACE_ID)).toBeVisible();
    const summary = page.getByRole("group", { name: "Trace summary" });
    await expect(summary.getByText("Spans", { exact: true })).toBeVisible();
    await expect(summary.getByText("3", { exact: true })).toBeVisible();
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
    await expect(page.getByRole("group", { name: "Trace summary" })).toBeVisible();
    await expect(page.getByRole("button", { name: /GET cache/ })).toBeVisible();
  });

  test("clicking a tab closes an open trace and shows the tab content", async ({ page }) => {
    await page.goto(`/traces?trace=${SEED_TRACE_ID}&tab=traces`);
    await expect(page.getByRole("group", { name: "Trace summary" })).toBeVisible();

    await page.getByRole("tab", { name: "Overview" }).click();

    // The workspace closes; the overview table takes its place.
    await expect(page).toHaveURL(/tab=overview/);
    await expect(page).not.toHaveURL(/[?&]trace=/);
    await expect(page.getByRole("group", { name: "Trace summary" })).not.toBeVisible();
    await expect(
      page.getByRole("row").filter({ hasText: SEED_SERVICE }),
    ).toBeVisible();
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

test.describe("trace inspect views (seeded data)", () => {
  const traceUrl = `/traces?trace=${SEED_TRACE_ID}&tab=traces`;

  test("summary bar shows trace stats and the service legend", async ({ page }) => {
    await page.goto(traceUrl);
    const summary = page.getByRole("group", { name: "Trace summary" });
    // The seeded trace has 2 error spans, so the Errors stat is present too.
    for (const label of ["Started", "Duration", "Spans", "Services", "Errors"]) {
      await expect(summary.getByText(label, { exact: true })).toBeVisible();
    }
    await expect(summary.getByText(SEED_SERVICE)).toBeVisible();
  });

  test("waterfall rows expose component and derived status", async ({ page }) => {
    await page.goto(traceUrl);
    // db.system.name drives the component icons.
    await expect(page.locator('[title="PostgreSQL"]').first()).toBeVisible();
    await expect(page.locator('[title="Redis"]').first()).toBeVisible();
    // Root span: OTel Error + http 500 → the status dot carries the code.
    await expect(page.locator('[title="status: 500"]')).toBeVisible();
  });

  test("waterfall subtree collapse hides children and shows +N", async ({ page }) => {
    await page.goto(traceUrl);
    await expect(page.getByRole("button", { name: /SELECT orders/ })).toBeVisible();

    await page.getByLabel("Collapse subtree").click();
    await expect(page.getByRole("button", { name: /SELECT orders/ })).not.toBeVisible();
    await expect(page.getByText("+2", { exact: true })).toBeVisible();

    await page.getByLabel("Expand subtree").click();
    await expect(page.getByRole("button", { name: /SELECT orders/ })).toBeVisible();
  });

  test("spans view shows the component column and derived status labels", async ({ page }) => {
    await page.goto(`${traceUrl}&view=spans`);
    const table = page.getByRole("table");
    await expect(table.getByText("Component", { exact: true })).toBeVisible();
    await expect(table.getByText("PostgreSQL")).toBeVisible();
    await expect(table.getByText("Redis")).toBeVisible();
    // Derived labels instead of raw OTel status: http code when present,
    // ERR/OK otherwise — "Unset" never reaches the user.
    await expect(table.getByText("500", { exact: true })).toBeVisible();
    await expect(table.getByText("ERR", { exact: true })).toBeVisible();
    await expect(table.getByText("OK", { exact: true })).toBeVisible();
    await expect(table.getByText("Unset")).not.toBeVisible();
  });

  test("tree view renders one card per span; clicking selects the span", async ({ page }) => {
    await page.goto(`${traceUrl}&view=graph`);
    for (const op of ["POST /checkout", "SELECT orders", "GET cache"]) {
      await expect(
        page.getByRole("button", { name: new RegExp(op.replace(/[/]/g, "\\/")) }),
      ).toBeVisible();
    }

    await page.getByRole("button", { name: /SELECT orders/ }).click();
    await expect(page.getByText("Span detail", { exact: true })).toBeVisible();
    await expect(page.getByText("connection refused").first()).toBeVisible();
  });
});
