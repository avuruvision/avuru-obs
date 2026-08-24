import { chromium, request, type FullConfig } from "@playwright/test";
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";

// Signs in once, before any spec, and saves the session for every test to
// reuse. The suite is written against an ADMIN view of a real, auth-enabled
// stack — that is what `make e2e-ui` brings up, and stubbing the session
// instead would test the UI against a hub that never checked anything.
//
// Doing it here rather than per-spec keeps one login for the whole run, and it
// is why the stack no longer has to be started with anonymous access on: the
// suite used to need an anonymous-admin override, which meant auth.spec.ts
// could not run beside it and the whole thing could not be a CI gate.
//
// auth.spec.ts opts out (`test.use({ storageState: … })` with an empty state):
// its subject is the signed-OUT experience.
export const STATE_PATH = "e2e/.auth/state.json";

const EMAIL = process.env.AVURUOBS_E2E_EMAIL ?? "admin";
// Pinned by `make e2e-ui`, which is the only supported way to run this suite.
const PASSWORD = process.env.AVURUOBS_AUTH_ADMIN_PASSWORD ?? "e2e-admin-pw";

export default async function globalSetup(config: FullConfig) {
  const baseURL = config.projects[0]?.use?.baseURL ?? "http://localhost:3001";

  const ctx = await request.newContext({ baseURL });
  const res = await ctx.post("/api/v1/auth/login", {
    data: { email: EMAIL, password: PASSWORD },
    // The hub rejects cross-origin writes by default; the UI origin is what a
    // browser would send.
    headers: { Origin: String(baseURL) },
  });
  if (!res.ok()) {
    throw new Error(
      `e2e global setup: login as ${EMAIL} failed with ${res.status()} ${res.statusText()}. ` +
        `Start the stack with \`make e2e-ui\`, which pins the admin password.`,
    );
  }
  const state = await ctx.storageState();
  await ctx.dispose();

  mkdirSync(dirname(STATE_PATH), { recursive: true });
  writeFileSync(STATE_PATH, JSON.stringify(state, null, 2));

  // Fail loudly here rather than leaving every spec to bounce to /login with
  // no explanation.
  const browser = await chromium.launch();
  const page = await browser.newContext({ storageState: STATE_PATH }).then((c) => c.newPage());
  const me = await page.request.get(`${baseURL}/api/v1/auth/me`);
  const body = await me.json();
  await browser.close();
  if (body?.user?.anonymous !== false) {
    throw new Error(`e2e global setup: saved session is not a real user (${JSON.stringify(body)})`);
  }
}
