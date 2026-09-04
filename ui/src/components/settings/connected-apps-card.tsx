"use client";

import { useState } from "react";
import { Plug, ShieldQuestion } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { useOAuthGrants, useRevokeOAuthGrant } from "@/hooks/use-oauth-grants";

// The applications this person has let read their estate, and the way to stop
// one.
//
// This card is what makes the consent screen's disclosure honest: telling
// someone that approving sends traces and log bodies to a model provider is
// only fair if they can change their mind afterwards. Disconnecting revokes
// the consent AND every token issued under it, so the application stops
// reading on its next request rather than whenever something expires.
export function ConnectedAppsCard() {
  const { data, isLoading } = useOAuthGrants(true);
  const revoke = useRevokeOAuthGrant();
  const [confirming, setConfirming] = useState<string | null>(null);

  const grants = data?.grants ?? [];
  // Nothing connected and nothing loading: the card would be an empty box on
  // the great majority of installs, where OAuth is off entirely.
  if (!isLoading && grants.length === 0) return null;

  return (
    <Card className="overflow-hidden" data-testid="connected-apps-card">
      <CardHeader>
        <CardTitle>
          <span className="inline-flex items-center gap-2">
            <Plug className="h-4 w-4 text-base-content/60" aria-hidden />
            Connected applications
          </span>
        </CardTitle>
        <span className="text-xs text-base-content/50">read your estate as you</span>
      </CardHeader>

      <div className="flex flex-col gap-3 border-t border-neutral p-4">
        <p className="flex items-start gap-1.5 text-xs text-base-content/60">
          <ShieldQuestion className="mt-0.5 h-3.5 w-3.5 shrink-0" aria-hidden />
          <span>
            These names were supplied by the applications themselves and are not
            verified. Disconnecting takes effect on the application&rsquo;s next
            request.
          </span>
        </p>

        <ul className="flex flex-col gap-2" data-testid="connected-apps-list">
          {grants.map((g) => (
            <li
              key={g.id}
              className="flex items-center justify-between gap-3 rounded-lg border border-neutral p-2.5"
            >
              <div className="flex min-w-0 flex-col">
                <span className="truncate text-sm">{g.clientName}</span>
                <span className="text-[11px] text-base-content/50">
                  project <span className="font-mono">{g.project}</span> ·{" "}
                  <span className="font-mono">{g.scopes}</span>
                </span>
              </div>
              {confirming === g.id ? (
                <div className="flex shrink-0 items-center gap-1.5">
                  <Button variant="ghost" size="sm" onClick={() => setConfirming(null)}>
                    Keep
                  </Button>
                  <Button
                    variant="danger"
                    size="sm"
                    disabled={revoke.isPending}
                    onClick={() => {
                      revoke.mutate(g.id);
                      setConfirming(null);
                    }}
                  >
                    Disconnect
                  </Button>
                </div>
              ) : (
                <Button
                  variant="ghost"
                  size="sm"
                  className="shrink-0"
                  onClick={() => setConfirming(g.id)}
                >
                  Disconnect
                </Button>
              )}
            </li>
          ))}
        </ul>
      </div>
    </Card>
  );
}
