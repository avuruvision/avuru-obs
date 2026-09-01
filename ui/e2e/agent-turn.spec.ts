import { test, expect } from "@playwright/test";

// Seeded fixture (deploy/compose/seed/fixtures/traces_genai.json): one agent
// turn under seed-assistant — an invoke_agent span containing two model calls
// and four tool executions, of which search_docs runs TWICE and one carries no
// gen_ai.tool.name.
const TURN_TRACE_ID = "abcd000aabcd000babcd000cabcd000d";
// An ordinary request with no gen_ai spans at all.
const PLAIN_TRACE_ID = "eeee5555ffff6666aaaa7777bbbb8888";

const turnUrl = (traceId: string) =>
  `/traces?tab=traces&trace=${traceId}&view=turn`;

const canvas = (page: import("@playwright/test").Page) =>
  page.getByTestId("agent-turn");

test.describe("agent turn", () => {
  test("draws the model calls and tools a turn is made of", async ({
    page,
  }) => {
    await page.goto(turnUrl(TURN_TRACE_ID));

    await expect(
      canvas(page).getByText("researcher", { exact: true }),
    ).toBeVisible();
    await expect(
      canvas(page).getByText("search_docs", { exact: true }),
    ).toBeVisible();
    await expect(
      canvas(page).getByText("run_sql", { exact: true }),
    ).toBeVisible();
    // The model that ANSWERED, not the alias that was asked for — the same
    // resolution the AI tables use.
    await expect(
      canvas(page).getByText("gpt-4o-2024-08-06", { exact: true }),
    ).toBeVisible();
  });

  test("a tool the turn hit twice is one node with a count", async ({
    page,
  }) => {
    await page.goto(turnUrl(TURN_TRACE_ID));

    // One card, not two: the loop is the thing worth seeing, and two identical
    // cards would say the same thing while hiding that it repeated.
    await expect(
      canvas(page).getByText("search_docs", { exact: true }),
    ).toHaveCount(1);
    await expect(canvas(page).getByText("2x").first()).toBeVisible();
  });

  test("a tool with no declared name falls back to its span name", async ({
    page,
  }) => {
    await page.goto(turnUrl(TURN_TRACE_ID));
    // Dropping it would understate how much tool work the turn did.
    await expect(
      canvas(page).getByText("tools/lookup_customer", { exact: true }),
    ).toBeVisible();
  });

  test("the tab is not offered on a trace with no turn", async ({ page }) => {
    await page.goto(turnUrl(PLAIN_TRACE_ID));

    // A deep link to ?view=turn on an ordinary request falls back to the
    // timeline rather than showing an empty panel, and the tab is hidden.
    await expect(
      page.getByRole("button", { name: "Turn", exact: true }),
    ).toHaveCount(0);
    await expect(canvas(page)).toHaveCount(0);
  });
});
