import { test, expect } from "@playwright/test";

// Boundaries on the map (design/2026-08-24-map-encoding.md): namespaces and
// service groups drawn as containers, so a reader can find their estate the way
// they think about it instead of scanning a force layout.
//
// The seeded fixtures declare `service.namespace` on three services
// (traces_declared.json), so namespace grouping produces real boxes here.
//
// Honest limitation, as everywhere on this screen: the box is drawn to a
// canvas. What the DOM can hold is the control, the URL, and the legend line
// that has to follow the choice.
test.describe("service map boundaries", () => {
  test("grouping is a choice, held in the URL", async ({ page }) => {
    await page.goto("/service-map");

    const select = page.getByRole("combobox", { name: "Group nodes by" });
    // Ungrouped by default: a box around every node is only clarifying once
    // you have asked for it.
    await expect(select).toHaveValue("none");
    await expect(page.getByTestId("map-legend")).not.toContainText("box =");

    await select.selectOption("namespace");
    await expect(page).toHaveURL(/groupBy=namespace/);
    await expect(page.getByTestId("map-legend")).toContainText("box = namespace");

    // The URL is the truth, not component state.
    await page.reload();
    await expect(page.getByRole("combobox", { name: "Group nodes by" })).toHaveValue("namespace");

    await page.getByRole("combobox", { name: "Group nodes by" }).selectOption("group");
    await expect(page).toHaveURL(/groupBy=group/);
    await expect(page.getByTestId("map-legend")).toContainText("box = service group");

    // Back to nothing clears the parameter rather than spelling out the default.
    await page.getByRole("combobox", { name: "Group nodes by" }).selectOption("none");
    await expect(page).not.toHaveURL(/groupBy=/);
  });

  test("service groups are only offered where a module computes them", async ({ page }) => {
    await page.route("**/api/v1/capabilities", (route) =>
      route.fulfill({ json: { version: "test", modules: ["core"] } }),
    );
    await page.goto("/service-map");

    const select = page.getByRole("combobox", { name: "Group nodes by" });
    await expect(select.getByRole("option", { name: "Namespace" })).toHaveCount(1);
    await expect(select.getByRole("option", { name: "Service group" })).toHaveCount(0);
  });

  test("the zoom controls have a number to move", async ({ page }) => {
    await page.goto("/service-map");

    // The opening value is whatever the layout's fit produced, not 100%, and
    // the readout rounds — so this holds the behaviour (it moves with the
    // controls, and the two are inverses) rather than exact arithmetic on a
    // rounded number.
    const readout = page.getByTestId("map-zoom");
    await expect(readout).toHaveText(/^\d+%$/);
    const percent = async () => Number((await readout.textContent())!.replace("%", ""));
    const start = await percent();

    await page.getByRole("button", { name: "Zoom in" }).click();
    await expect.poll(percent).toBeGreaterThan(start);

    await page.getByRole("button", { name: "Zoom out" }).click();
    await expect.poll(percent).toBeLessThanOrEqual(start);
  });
});
