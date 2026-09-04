import { test, expect } from "@playwright/test";

// Seeded fixture (deploy/compose/seed/fixtures/traces_multiservice.json):
// seed-gateway makes a client call that seed-payments serves. That pair is the
// only dependency the seed guarantees, so the dependency assertions use it.
const CALLER = "seed-gateway";
const CALLEE = "seed-payments";

test.describe("service detail", () => {
  test("summarises one service and draws its neighbourhood", async ({ page }) => {
    await page.goto(`/services?service=${CALLEE}`);

    await expect(page.getByRole("heading", { name: CALLEE })).toBeVisible();
    // The four RED tiles carry the headline numbers.
    for (const label of ["Rate", "Errors", "p95", "p99"]) {
      await expect(page.getByText(label, { exact: true }).first()).toBeVisible();
    }

    // The diagram is the default view: callers on the left, this service in the
    // middle. Unlike the service map it is SVG and HTML, not canvas, so what it
    // claims is assertable here rather than only visible to a human.
    const diagram = page.getByTestId("service-neighbourhood");
    await expect(diagram).toBeVisible();
    await expect(diagram.getByText(CALLER)).toBeVisible();
    await expect(diagram.getByText(CALLEE)).toBeVisible();
  });

  test("the table view names both sides of the neighbourhood", async ({ page }) => {
    await page.goto(`/services?service=${CALLEE}&deps=table`);

    // Callers and callees are shown apart: who is affected when this breaks,
    // against what could be breaking it.
    const calledBy = page.locator("div").filter({ hasText: /^Called by/ }).first();
    await expect(calledBy.getByText(CALLER)).toBeVisible();
  });

  test("a caller in the diagram opens that service's own page", async ({ page }) => {
    await page.goto(`/services?service=${CALLEE}`);
    await page
      .getByTestId("service-neighbourhood")
      .getByRole("button", { name: CALLER })
      .click();
    await expect(page).toHaveURL(new RegExp(`service=${CALLER}`));
    await expect(page.getByRole("heading", { name: CALLER })).toBeVisible();
  });

  test("a caller in the table opens that service's own page", async ({ page }) => {
    await page.goto(`/services?service=${CALLEE}&deps=table`);
    await page.getByRole("row").filter({ hasText: CALLER }).click();
    await expect(page).toHaveURL(new RegExp(`service=${CALLER}`));
    await expect(page.getByRole("heading", { name: CALLER })).toBeVisible();
  });

  test("the diagram/table choice is url state", async ({ page }) => {
    await page.goto(`/services?service=${CALLEE}`);
    await expect(page.getByTestId("service-neighbourhood")).toBeVisible();

    await page.getByRole("button", { name: "Table" }).click();
    await expect(page).toHaveURL(/deps=table/);
    await expect(page.getByRole("row").filter({ hasText: CALLER })).toBeVisible();

    // The diagram is the default, so choosing it CLEARS the param rather than
    // writing deps=diagram — a plain link to a service still arrives on it.
    await page.getByRole("button", { name: "Diagram" }).click();
    await expect(page).not.toHaveURL(/deps=/);
    await expect(page.getByTestId("service-neighbourhood")).toBeVisible();
  });

  test("the signal tabs are scoped to the service", async ({ page }) => {
    await page.goto(`/services?service=${CALLEE}&view=traces`);
    // Every listed trace involves this service — the tab reuses the trace list
    // with a service filter rather than re-implementing one.
    await expect(page.getByText(CALLEE).first()).toBeVisible();

    await page.goto(`/services?service=${CALLEE}&view=logs`);
    // Logs either list or say plainly that there are none; both are correct,
    // and neither is an error state.
    await expect(
      page.getByText(/open in Logs|No logs in this window/),
    ).toBeVisible();
  });

  test("a name with nothing behind it says so", async ({ page }) => {
    await page.goto("/services?service=does-not-exist");
    await expect(page.getByText("No data for does-not-exist")).toBeVisible();
    // And the way back is always present. It is a button, not a link: the
    // selection is url state on this same screen, and the App Router skips a
    // link to the pathname it is already on — leaving ?service= in place.
    await page.getByRole("button", { name: "All services" }).click();
    await expect(page).not.toHaveURL(/service=/);
  });
});
