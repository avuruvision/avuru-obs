//go:build e2e

// What the FRONT DOOR serves, as opposed to what the hub serves.
//
// Every other case in this suite talks to the hub directly, because compose
// publishes it. Real installs do not: an Ingress and the UI's nginx sit in
// front, and a path neither of them routes falls through to the static export
// — which answers with HTML and a 200. Two shipped features reached nobody
// that way. `POST /mcp` was unreachable on every chart install from v0.12 until
// v0.14, and the OAuth discovery documents, added in v0.14, were unreachable on
// the day they shipped.
//
// Both were invisible for the same reason: the one place they were exercised
// was the one place the routing does not apply. So these cases go through
// nginx on purpose, and they are the only ones here that do.
package e2e

import (
	"net/http"
	"strings"
	"testing"
)

// The invariant, and it holds whether or not this stack runs OAuth: a protocol
// endpoint must never be answered by the UI. What the hub replies is not the
// point — 200 with a document, 401 for a missing credential, 404 for a module
// that is off are all correct answers from the right process. HTML is the
// symptom, because it means nginx served the export and the request never
// arrived.
//
// It is also the worse failure of the two: a client parsing JSON fails deeper
// on 200-plus-HTML than it would on a clean 404, which is what made the OAuth
// half of this so quiet.
func assertNotServedByTheUI(t *testing.T, method, path string) {
	t.Helper()
	req, err := http.NewRequest(method, uiURL+path, nil)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s through the UI: %v", method, path, err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); strings.HasPrefix(ct, "text/html") {
		t.Errorf("%s %s answered %d %s from the UI's static export — nginx does not route it to the hub, so no client can reach it",
			method, path, resp.StatusCode, ct)
	}
}

// /mcp is not under /api, so no API rule covers it.
func TestMCPEndpointIsRoutedThroughTheFrontDoor(t *testing.T) {
	assertNotServedByTheUI(t, http.MethodPost, "/mcp")
}

// RFC 8414 and RFC 9728 fix these at the ORIGIN ROOT, and a client fetches them
// before it holds any credential — so a miss breaks the flow at its first step,
// with nothing yet in hand to produce a better error from.
func TestOAuthDiscoveryIsRoutedThroughTheFrontDoor(t *testing.T) {
	for _, path := range []string{
		"/.well-known/oauth-authorization-server",
		"/.well-known/oauth-protected-resource",
		"/.well-known/oauth-protected-resource/mcp",
	} {
		t.Run(path, func(t *testing.T) {
			assertNotServedByTheUI(t, http.MethodGet, path)
		})
	}
}
