"use client";

import { useEffect, useState } from "react";
import { AlertTriangle, ShieldQuestion } from "lucide-react";
import { apiGet, apiPost } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { CenteredSpinner } from "@/components/ui/spinner";
import type { ConsentView } from "@/lib/api-types";

// What a person is actually agreeing to.
//
// The disclosure here is not decoration and must not be collapsed behind a
// "details" toggle: approving sends traces and LOG BODIES out of this
// installation to whatever model provider sits behind the application asking.
// That is the same sentence values.yaml puts in front of an operator turning
// the MCP module on, in front of the person whose data it is.
//
// The client's name is whatever it typed when it registered. Registration is
// unauthenticated by necessity, so the name is attacker-controlled and is
// presented as unverified — with the redirect host beside it, because the host
// is the one fact a reader can actually check.
export function ConsentCard() {
  const [view, setView] = useState<ConsentView | null>(null);
  const [project, setProject] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  // The authorize parameters ride in the URL: the hub re-validates them on the
  // POST, so this page carries them rather than holding server-side state.
  const query = typeof window === "undefined" ? "" : window.location.search;

  useEffect(() => {
    apiGet<ConsentView>(`/api/v1/auth/oauth/consent${query}`)
      .then((v) => {
        setView(v);
        setProject(v.defaultProject);
      })
      .catch((e) => setError(e instanceof Error ? e.message : "This request could not be read."));
  }, [query]);

  async function decide(approve: boolean) {
    setBusy(true);
    setError(null);
    try {
      const res = await apiPost<{ redirect: string }>(
        `/api/v1/auth/oauth/consent${query}`,
        { approve, project },
      );
      // The hub built this redirect from the REGISTERED redirect URI, never
      // from anything the page supplied.
      window.location.assign(res.redirect);
    } catch (e) {
      setError(e instanceof Error ? e.message : "The decision could not be recorded.");
      setBusy(false);
    }
  }

  if (error && !view) {
    return (
      <div className="mx-auto mt-16 max-w-lg px-4">
        <Card className="flex flex-col gap-2 p-6">
          <h1 className="text-sm font-semibold text-error">This request cannot be shown</h1>
          <p className="text-xs text-base-content/60">{error}</p>
        </Card>
      </div>
    );
  }
  if (!view) return <CenteredSpinner />;

  return (
    <div className="mx-auto mt-12 max-w-lg px-4">
      <Card className="flex flex-col gap-4 p-6" data-testid="oauth-consent">
        <div className="flex flex-col gap-1">
          <h1 className="text-base font-semibold text-primary">
            Give <span data-testid="consent-client">{view.clientName}</span> access?
          </h1>
          {/* Never presented as verified: see the component comment. */}
          <p className="flex items-start gap-1.5 text-xs text-base-content/55">
            <ShieldQuestion className="mt-0.5 h-3.5 w-3.5 shrink-0" aria-hidden />
            <span data-testid="consent-unverified">
              This name was supplied by the application and has not been verified. It will
              send you back to <span className="font-mono">{view.redirectHost}</span>.
            </span>
          </p>
        </div>

        <div className="flex flex-col gap-1 rounded-lg border border-neutral p-3">
          <p className="text-xs font-medium">It will be able to read, as you:</p>
          <p className="text-xs text-base-content/70">
            Traces, log bodies, error issues and service health in the project you choose —
            the same data you can see in the app, and nothing more.
          </p>
        </div>

        {/* The sentence this whole screen exists for. */}
        <div
          className="flex items-start gap-2 rounded-lg border border-warning/50 bg-warning/10 p-3"
          data-testid="consent-disclosure"
        >
          <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0 text-warning" aria-hidden />
          <div className="flex flex-col gap-1 text-xs">
            <p className="font-medium">This sends data out of this installation.</p>
            <p className="text-base-content/70">
              Approving lets this application pull traces and{" "}
              <strong>log bodies</strong> out of your cluster and into whichever model
              provider is behind it. Log bodies are where user data lives.
            </p>
            <p className="text-base-content/70">
              Every request it makes is recorded with your name, the tool and its
              arguments — never the data returned.
            </p>
          </div>
        </div>

        <label className="flex flex-col gap-1 text-xs">
          <span className="font-medium">Project</span>
          <select
            className="rounded-lg border border-neutral bg-base-100 px-2 py-1.5 text-xs"
            value={project}
            onChange={(e) => setProject(e.target.value)}
            data-testid="consent-project"
          >
            {view.projects.map((p) => (
              <option key={p} value={p}>
                {p}
              </option>
            ))}
          </select>
          <span className="text-base-content/50">
            Access is limited to this one project, and to what you can already read in it.
          </span>
        </label>

        {error && <p className="text-xs text-error">{error}</p>}

        {/* Cancel first and autofocused: the safe choice should be the easy one. */}
        <div className="flex items-center justify-end gap-2">
          <Button variant="ghost" size="sm" autoFocus disabled={busy} onClick={() => decide(false)}>
            Cancel
          </Button>
          <Button size="sm" disabled={busy} onClick={() => decide(true)} data-testid="consent-approve">
            Approve
          </Button>
        </div>
      </Card>
    </div>
  );
}
