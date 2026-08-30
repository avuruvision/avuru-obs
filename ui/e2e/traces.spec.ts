import { test, expect, type Page } from "@playwright/test";

// Seeded fixtures (deploy/compose/seed/fixtures): one deterministic
// single-service trace plus one gateway→payments multi-service trace.
// NOTE: the overview groups rows under per-service header rows, so row
// lookups filter by OPERATION text (unique to data rows), not service name.
const SEED_TRACE_ID = "aaaa1111bbbb2222cccc3333dddd4444";
const SEED_SERVICE = "seed-checkout";
const SEED_ERROR_SPAN_ID = "b2c3d4e5f6071829";
const MULTI_TRACE_ID = "eeee5555ffff6666aaaa7777bbbb8888";
const MULTI_ROOT_SERVICE = "seed-gateway";
const MULTI_CHILD_SERVICE = "seed-payments";

test.describe("shell", () => {
  test("renders sidebar nav and toggles theme", async ({ page }) => {
    await page.goto("/traces");

    await expect(page.getByRole("link", { name: "Avuru Obs", exact: true })).toBeVisible();
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
    for (const section of ["Topology", "Signals", "Operations", "Infrastructure", "System"]) {
      await expect(sidebar.getByText(section, { exact: true })).toBeVisible();
    }
    // Masthead shows a breadcrumb derived from the route.
    await expect(page.getByLabel("Breadcrumb")).toContainText("Traces");
  });

  test("service map renders the seeded service graph", async ({ page }) => {
    await page.goto("/service-map");
    // Seeded data → not the empty state; the screen summarises the nodes.
    await expect(page.getByText(/click a service for its traces/)).toBeVisible();
  });
});

test.describe("traces screen (seeded data)", () => {
  test("overview lists the seeded operation with its error rate", async ({ page }) => {
    await page.goto("/traces");

    const row = page.getByRole("row").filter({ hasText: "POST /checkout" });
    await expect(row).toBeVisible();
    await expect(row).toContainText(SEED_SERVICE);
    // The seeded root span is an error → the row carries an error badge.
    await expect(row.locator("text=%")).toBeVisible();
  });

  test("overview groups operations under per-service headers", async ({ page }) => {
    await page.goto("/traces");

    // Three seeded services in the default project → one header row each.
    for (const svc of [SEED_SERVICE, MULTI_ROOT_SERVICE, MULTI_CHILD_SERVICE]) {
      await expect(
        page.getByRole("row").filter({ hasText: svc }).first(),
      ).toBeVisible();
    }
    // The child service's entry operation is a data row of its own.
    await expect(page.getByRole("row").filter({ hasText: "POST /pay" })).toBeVisible();
  });

  test("drill down: overview row → filtered list → waterfall → span detail", async ({ page }) => {
    await page.goto("/traces");

    // Row click filters to the operation and switches to the Traces tab.
    await page.getByRole("row").filter({ hasText: "POST /checkout" }).click();
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
      page.getByRole("row").filter({ hasText: "POST /checkout" }),
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

  test("service filter matches participating services, root stays visible", async ({ page }) => {
    // seed-payments never roots a trace: the gateway does. The filter must
    // still surface the trace, listed under its root service.
    await page.goto(`/traces?service=${MULTI_CHILD_SERVICE}&tab=traces`);

    const row = page.getByRole("row").filter({ hasText: "GET /api/pay" });
    await expect(row).toBeVisible();
    await expect(row).toContainText(MULTI_ROOT_SERVICE);
  });

  test("overview for a non-root service shows its ops and drills into traces", async ({ page }) => {
    await page.goto(`/traces?service=${MULTI_CHILD_SERVICE}`);

    // The child service's own entry-span operation, not "no root spans".
    const opRow = page.getByRole("row").filter({ hasText: "POST /pay" });
    await expect(opRow).toBeVisible();

    // Drill-down: service+operation land on a non-empty trace list.
    await opRow.click();
    await expect(page).toHaveURL(/tab=traces/);
    await expect(
      page.getByRole("row").filter({ hasText: "GET /api/pay" }),
    ).toBeVisible();
  });

  test("span id search opens the containing trace with the span selected", async ({ page }) => {
    await page.goto("/traces");

    const input = page.getByLabel("Open trace or span by id");
    await input.fill(SEED_ERROR_SPAN_ID);
    await input.press("Enter");

    await expect(page).toHaveURL(new RegExp(`trace=${SEED_TRACE_ID}`));
    await expect(page).toHaveURL(new RegExp(`span=${SEED_ERROR_SPAN_ID}`));
    // The selected span's detail panel opens on the failing SQL span.
    await expect(page.getByText("Span detail", { exact: true })).toBeVisible();
    await expect(page.getByText("connection refused").first()).toBeVisible();
  });

  test("unknown span id shows an inline error and stays on the list", async ({ page }) => {
    await page.goto("/traces");

    const input = page.getByLabel("Open trace or span by id");
    await input.fill("0000000000000000");
    await input.press("Enter");

    await expect(page.getByText("No span found with this id.")).toBeVisible();
    await expect(page).not.toHaveURL(/[?&]trace=/);
  });

  test("service filter autocompletes seeded service names", async ({ page }) => {
    await page.goto("/traces?tab=traces");

    const combo = page.getByRole("combobox", { name: "Filter by service" });
    await combo.click();
    await combo.fill("seed");

    // Suggestions come from /api/v1/services; picking one applies it as a filter.
    const listbox = page.getByRole("listbox", { name: "Filter by service" });
    await expect(listbox.getByRole("option", { name: SEED_SERVICE, exact: true })).toBeVisible();
    await listbox.getByRole("option", { name: SEED_SERVICE, exact: true }).click();

    await expect(page).toHaveURL(new RegExp(`service=${SEED_SERVICE}`));
    await expect(page.getByRole("row").filter({ hasText: "POST /checkout" })).toBeVisible();
  });

  test("operation filter autocompletes seeded operations (GET/POST)", async ({ page }) => {
    await page.goto("/traces?tab=traces");

    const combo = page.getByRole("combobox", { name: "Filter by operation" });
    await combo.click();
    await combo.fill("POST");

    // Operation suggestions come from the overview (all services); pick one.
    const listbox = page.getByRole("listbox", { name: "Filter by operation" });
    await expect(listbox.getByRole("option", { name: "POST /checkout", exact: true })).toBeVisible();
    await listbox.getByRole("option", { name: "POST /checkout", exact: true }).click();

    await expect(page).toHaveURL(/operation=/);
    await expect(page.getByRole("row").filter({ hasText: "POST /checkout" })).toBeVisible();
  });

  test("cleared filters stay cleared after navigating away and back", async ({ page }) => {
    // Regression: the static-export segment cache resurrects the hydration
    // query string when a bare sidebar <Link> re-enters the route — cleared
    // filters came back after visiting another screen (see nav-url-guard.tsx).
    await page.goto(`/traces?service=${SEED_SERVICE}&status=error&tab=traces`);
    const combo = page.getByRole("combobox", { name: "Filter by service" });
    await expect(combo).toHaveValue(SEED_SERVICE);

    await page.getByRole("button", { name: "Clear" }).click();
    await expect(page).not.toHaveURL(/[?&]service=/);

    await page.getByRole("link", { name: "Logs", exact: true }).click();
    await expect(page).toHaveURL(/\/logs/);
    await page.getByRole("link", { name: "Traces", exact: true }).click();
    await expect(page).toHaveURL(/\/traces/);

    await expect(page).not.toHaveURL(/[?&]service=/);
    await expect(combo).toHaveValue("");
  });

  test("group-by-service toggle sections and collapses the trace list", async ({ page }) => {
    await page.goto("/traces?tab=traces");

    const checkoutRow = page.getByRole("row").filter({ hasText: "POST /checkout" });
    await expect(checkoutRow.first()).toBeVisible();

    await page.getByRole("checkbox", { name: "Group by service" }).check();

    // A per-service header row (name + trace count) appears for each root service.
    const header = (svc: string) =>
      page.getByRole("row").filter({ hasText: svc }).filter({ hasText: /trace/ });
    await expect(header(SEED_SERVICE)).toBeVisible();
    await expect(header(MULTI_ROOT_SERVICE)).toBeVisible();

    // Collapsing the checkout section hides its data row.
    await header(SEED_SERVICE).click();
    await expect(checkoutRow).toHaveCount(0);
  });
});

test.describe("global time range", () => {
  test("selected range survives sidebar navigation", async ({ page }) => {
    await page.goto("/traces");

    await page.getByRole("button", { name: "1h", exact: true }).click();
    await expect(page).toHaveURL(/range=1h/);

    await page.getByRole("link", { name: "Logs", exact: true }).click();
    // localStorage bridges the bare-path navigation, then TimeRangeSync
    // re-materializes ?range= for shareability.
    await expect(page.getByRole("button", { name: "1h", exact: true })).toHaveAttribute(
      "aria-pressed",
      "true",
    );
    await expect(page).toHaveURL(/range=1h/);
  });
});

test.describe("service map controls", () => {
  test("re-layout button re-runs the graph layout", async ({ page }) => {
    await page.goto("/service-map");

    const button = page.getByRole("button", { name: "Re-run layout" });
    await expect(button).toBeVisible();
    await button.click();
    // The graph stays mounted and interactive after the re-layout.
    await expect(page.getByText(/click a service for its traces/)).toBeVisible();
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

test.describe("service perspective from a trace (seeded data)", () => {
  const traceUrl = `/traces?trace=${MULTI_TRACE_ID}&tab=traces`;
  const gatewayRow = (page: Page) =>
    page.getByRole("button", { name: /GET \/api\/pay/ });
  const paymentsRow = (page: Page) =>
    page.getByRole("button", { name: /POST \/pay/ });

  test("legend chip toggles focus: other services dim, URL is shareable", async ({ page }) => {
    await page.goto(traceUrl);
    await expect(gatewayRow(page)).toBeVisible();

    const chip = page.getByRole("button", { name: MULTI_CHILD_SERVICE, exact: true });
    await chip.click();
    await expect(page).toHaveURL(new RegExp(`focus=${MULTI_CHILD_SERVICE}`));
    await expect(chip).toHaveAttribute("aria-pressed", "true");
    await expect(gatewayRow(page)).toHaveClass(/opacity-40/);
    await expect(paymentsRow(page)).not.toHaveClass(/opacity-40/);

    // Second click clears the focus.
    await chip.click();
    await expect(page).not.toHaveURL(/[?&]focus=/);
    await expect(gatewayRow(page)).not.toHaveClass(/opacity-40/);
  });

  test("?focus= deep link applies dimming on load", async ({ page }) => {
    await page.goto(`${traceUrl}&focus=${MULTI_CHILD_SERVICE}`);
    await expect(gatewayRow(page)).toHaveClass(/opacity-40/);
    await expect(paymentsRow(page)).not.toHaveClass(/opacity-40/);
  });

  test("chip menu jumps to the service's participant-filtered traces", async ({ page }) => {
    await page.goto(traceUrl);
    await expect(gatewayRow(page)).toBeVisible();

    await page.getByRole("button", { name: `${MULTI_CHILD_SERVICE} actions` }).click();
    await page.getByRole("menuitem", { name: "View service traces" }).click();

    await expect(page).toHaveURL(new RegExp(`service=${MULTI_CHILD_SERVICE}`));
    await expect(page).not.toHaveURL(/[?&]trace=/);
    await expect(page.getByRole("group", { name: "Trace summary" })).not.toBeVisible();
    // The gateway-rooted trace is listed because seed-payments participates.
    const row = page.getByRole("row").filter({ hasText: "GET /api/pay" });
    await expect(row).toBeVisible();
    await expect(row).toContainText(MULTI_ROOT_SERVICE);
  });

  test("span detail links to service traces and the operation's traces", async ({ page }) => {
    await page.goto(traceUrl);
    await paymentsRow(page).click();

    await expect(page.getByText("Span detail", { exact: true })).toBeVisible();
    await page.getByRole("button", { name: "Traces of this operation" }).click();

    await expect(page).toHaveURL(new RegExp(`service=${MULTI_CHILD_SERVICE}`));
    await expect(page).toHaveURL(/operation=POST/);
    await expect(page).not.toHaveURL(/[?&]trace=/);
    await expect(
      page.getByRole("row").filter({ hasText: "GET /api/pay" }),
    ).toBeVisible();
  });
});

test.describe("span detail panel (seeded data)", () => {
  const LONG_VALUE = "SELECT * FROM orders WHERE id = ?";

  async function openSpanDetail(page: Page) {
    await page.goto(`/traces?trace=${SEED_TRACE_ID}&tab=traces`);
    await page.getByRole("button", { name: /SELECT orders/ }).click();
    await expect(page.getByText("Span detail", { exact: true })).toBeVisible();
  }

  test("attribute values stay readable next to long keys", async ({ page }) => {
    await openSpanDetail(page);
    // The seeded span carries a 50-char key; pre-fix it crushed the value
    // column to ~1 character per line.
    await expect(
      page.getByText("db.client.connections.wait_time_before_timeout_ms"),
    ).toBeVisible();
    const value = page.getByText(LONG_VALUE).first();
    await expect(value).toBeVisible();
    const box = (await value.boundingBox())!;
    expect(box.width).toBeGreaterThan(120);
    expect(box.height).toBeLessThan(60);
  });

  test("attribute value copies to clipboard", async ({ page, context }) => {
    await context.grantPermissions(["clipboard-read", "clipboard-write"]);
    await openSpanDetail(page);
    await page.getByText(LONG_VALUE).first().hover();
    await page.getByRole("button", { name: "Copy db.query.text" }).click();
    // readText() needs the document focused and the async write settled, and
    // neither is guaranteed the instant the click returns — a single read here
    // is a flake waiting for a busy machine. Poll instead, now that this suite
    // is a gate rather than something run by hand.
    await page.bringToFront();
    await expect
      .poll(() => page.evaluate(() => navigator.clipboard.readText()))
      .toBe(LONG_VALUE);
  });

  test("panel resizes by drag, persists, and resets on double-click", async ({ page }) => {
    await openSpanDetail(page);
    const aside = page.locator("aside").filter({ hasText: "Span detail" });
    const handle = page.getByRole("separator", { name: "Resize span detail" });

    const before = (await aside.boundingBox())!;
    const h = (await handle.boundingBox())!;
    await page.mouse.move(h.x + h.width / 2, h.y + h.height / 2);
    await page.mouse.down();
    await page.mouse.move(h.x - 150, h.y + h.height / 2, { steps: 5 });
    await page.mouse.up();
    const after = (await aside.boundingBox())!;
    expect(after.width).toBeGreaterThan(before.width + 100);

    await page.reload();
    await expect(page.getByText("Span detail", { exact: true })).toBeVisible();
    const reloaded = (await aside.boundingBox())!;
    expect(Math.abs(reloaded.width - after.width)).toBeLessThan(10);

    await handle.dblclick();
    await expect
      .poll(async () => (await aside.boundingBox())!.width)
      .toBeLessThan(after.width);
  });

  test("expand overlay opens; Esc closes it without leaving fullscreen", async ({ page }) => {
    await openSpanDetail(page);
    await page.getByRole("button", { name: "Full screen" }).click();
    await page.getByRole("button", { name: "Expand span detail" }).click();

    const dialog = page.getByRole("dialog", { name: "Span detail expanded" });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByText(LONG_VALUE)).toBeVisible();

    await page.keyboard.press("Escape");
    await expect(dialog).not.toBeVisible();
    await expect(page.getByRole("button", { name: "Exit full screen" })).toBeVisible();

    await page.keyboard.press("Escape");
    await expect(page.getByRole("button", { name: "Full screen" })).toBeVisible();
  });
});
