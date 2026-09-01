import { test, expect } from "@playwright/test";

// Seeded fixture (deploy/compose/seed/fixtures/traces_genai.json):
//  - seed-assistant calls gpt-4o (answering as gpt-4o-2024-08-06) twice, once
//    cut off at the token ceiling, plus claude-sonnet on the OLDER token
//    spelling and with only a requested model.
//  - seed-search calls text-embedding-3-small, and makes one gpt-4o call that
//    fails and reports no usage at all.
//  - seed-assistant also runs ONE AGENT TURN: an invoke_agent span containing
//    two model calls and four tool executions, of which search_docs runs twice
//    and one carries no gen_ai.tool.name. Before v0.12 all six of those spans
//    counted as calls to a model.
// Prices (deploy/compose/ai.json) are deliberately PARTIAL: gpt-4o and the
// embedding model are priced, claude-sonnet is not.
const RESPONDING_MODEL = "gpt-4o-2024-08-06";
const UNPRICED_MODEL = "claude-sonnet";
const CALLER = "seed-assistant";

test.describe("AI observability", () => {
  test("lists the models the estate is calling", async ({ page }) => {
    await page.goto("/ai");

    const models = page.getByTestId("ai-models");
    await expect(models.getByText(RESPONDING_MODEL)).toBeVisible();
    await expect(models.getByText(UNPRICED_MODEL)).toBeVisible();
    // Grouped by what ANSWERED: the alias that was requested is not a row of
    // its own beside the build that served it.
    await expect(models.getByText("gpt-4o", { exact: true })).toHaveCount(0);

    await expect(page.getByTestId("ai-calls")).toBeVisible();
    await expect(page.getByTestId("ai-tokens")).toContainText("in");
  });

  test("prices what it can and says the total is a floor", async ({ page }) => {
    await page.goto("/ai");

    // gpt-4o is priced; the model that answered is gpt-4o-2024-08-06, so this
    // cost came from a prefix rule and is marked as inferred.
    const row = page
      .getByTestId("ai-models")
      .getByRole("row")
      .filter({ hasText: RESPONDING_MODEL });
    await expect(row).toContainText("≈");

    // The unpriced model is named rather than costed at zero.
    await expect(page.getByTestId("ai-coverage")).toContainText(UNPRICED_MODEL);
    await expect(page.getByTestId("ai-coverage")).toContainText("floor");
  });

  test("a call that reported no usage is counted, not averaged in as zero", async ({
    page,
  }) => {
    await page.goto("/ai");
    await expect(page.getByTestId("ai-coverage")).toContainText(
      "no token usage",
    );
  });

  test("truncation is reported apart from failure", async ({ page }) => {
    await page.goto("/ai");
    const models = page.getByTestId("ai-models");
    await expect(
      models.getByRole("columnheader", { name: "Truncated" }),
    ).toBeVisible();
    await expect(
      models.getByRole("columnheader", { name: "Failed" }),
    ).toBeVisible();
  });

  test("tools are reported apart from model calls", async ({ page }) => {
    await page.goto("/ai");
    const tools = page.getByTestId("ai-tools");

    // The repeated tool is ONE row with a count of 2 — the loop is the thing
    // worth seeing, not two identical lines.
    const search = tools.getByRole("row").filter({ hasText: "search_docs" });
    await expect(search).toHaveCount(1);
    await expect(search).toContainText("2");

    // The tool whose instrumentation named nothing is shown under its span
    // name, and labelled so it does not read as a name the SDK supplied.
    const fallback = tools
      .getByRole("row")
      .filter({ hasText: "tools/lookup_customer" });
    await expect(fallback).toHaveCount(1);
    await expect(fallback).toContainText("span name");
  });

  // The v0.12 defect, on the screen. A tool execution is not a model call, so
  // no tool name may appear in the model table and the model table must carry
  // no row without a model name.
  test("tool calls stay out of the model table", async ({ page }) => {
    await page.goto("/ai");
    const models = page.getByTestId("ai-models");
    await expect(models.getByText("search_docs")).toHaveCount(0);
    await expect(models.getByText("run_sql")).toHaveCount(0);
    await expect(
      models.getByRole("row").filter({ hasText: "invoke_agent" }),
    ).toHaveCount(0);
  });

  test("spend has an owner", async ({ page }) => {
    await page.goto("/ai");
    // One row per (service, model): the assistant calls two models, so naming
    // the service alone would match twice — which is the point of the view.
    const row = page
      .getByTestId("ai-callers")
      .getByRole("row")
      .filter({ hasText: CALLER })
      .filter({ hasText: RESPONDING_MODEL });
    await expect(row).toHaveCount(1);
  });

  // The decisive gate for the content decision. The fixture DOES carry prompt
  // and completion text; the gateway drops it on the way in. What must survive
  // is everything beside it — the token counts above all, which live under a
  // key the pattern is anchored to spare.
  test("the gateway drops prompt text and keeps the token counts", async ({
    page,
  }) => {
    const res = await page.request.get(
      "/api/v1/traces/abcd0001abcd0002abcd0003abcd0004",
    );
    expect(res.ok()).toBeTruthy();
    const trace = await res.json();
    const span = trace.spans[0];
    const keys = Object.keys(span.attributes ?? {});

    expect(keys).not.toContain("gen_ai.prompt.0.content");
    expect(keys).not.toContain("gen_ai.completion.0.content");
    expect(JSON.stringify(span.attributes)).not.toContain(
      "summarise the last quarter",
    );
    // …and the call is still fully described.
    expect(span.attributes["gen_ai.usage.input_tokens"]).toBe("1200");
    expect(span.attributes["gen_ai.response.model"]).toBe("gpt-4o-2024-08-06");
  });

  test("an install that redacts is not warned about content", async ({
    page,
  }) => {
    await page.goto("/ai");
    await expect(page.getByTestId("ai-models")).toBeVisible();
    // The warning exists for installs where content got through. Here it did
    // not, and a banner that cries wolf is worse than no banner.
    await expect(page.getByTestId("ai-content-warning")).toHaveCount(0);
  });
});
