import { test, expect } from "@playwright/test";

// What the FRONT DOOR serves, as opposed to what the hub serves.
//
// This suite's baseURL is the UI's nginx, which is exactly the layer that hid
// two shipped routing bugs. The Go e2e suite talks to the hub directly (compose
// publishes it), so a path that nginx and the Ingress fail to route looks
// perfectly healthy there while reaching nobody in a real install. `POST /mcp`
// was unreachable on every chart install from v0.12 until v0.14, and the OAuth
// discovery documents were unreachable on the day they shipped.
//
// So these cases deliberately assert nothing about the UI. They live here only
// because this is the suite that runs behind the front door.

// Protocol endpoints cannot live under /api: an MCP client is configured with a
// bare server URL, and RFC 8414/9728 fix the discovery documents at the origin
// root. Each therefore needs its own routing rule, and each is one someone can
// forget.
const PROTOCOL_ENDPOINTS = [
  { method: "POST" as const, path: "/mcp" },
  { method: "GET" as const, path: "/.well-known/oauth-authorization-server" },
  { method: "GET" as const, path: "/.well-known/oauth-protected-resource" },
  { method: "GET" as const, path: "/.well-known/oauth-protected-resource/mcp" },
];

test.describe("front door routing", () => {
  // The invariant holds whether or not this stack runs OAuth, which is what
  // makes it a guard rather than a snapshot. What the hub answers is not the
  // point — a document, a 401 for a missing credential, a 404 for a module that
  // is off are all correct answers from the right process. HTML is the symptom:
  // it means nginx served the static export and the request never arrived.
  for (const { method, path } of PROTOCOL_ENDPOINTS) {
    test(`${method} ${path} reaches the hub, not the static export`, async ({ request }) => {
      const response = await request.fetch(path, { method, failOnStatusCode: false });
      const contentType = response.headers()["content-type"] ?? "";

      expect(
        contentType,
        `${method} ${path} answered ${response.status()} ${contentType} — nginx did not route it to the hub, so no client can reach it`,
      ).not.toContain("text/html");
    });
  }
});
