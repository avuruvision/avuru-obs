"use client";

import { useState } from "react";
import { Plus, Send, Pencil, Trash2 } from "lucide-react";
import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { CenteredSpinner } from "@/components/ui/spinner";
import { ApiError } from "@/lib/api";
import {
  useAlertChannels,
  useCreateChannel,
  useUpdateChannel,
  useDeleteChannel,
  useTestChannel,
} from "@/hooks/use-alerts-data";
import { ChannelForm } from "./channel-form";

// Notification channels: the delivery endpoints alert rules reference by name.
// UI-managed channels are editable here; config channels (ConfigMap) are
// read-only and shadowed when a same-name UI channel exists. Channels are
// instance-global — the webhook payload carries the tenant/environment.
export function ChannelsPanel() {
  const { data, isLoading } = useAlertChannels();
  const create = useCreateChannel();
  const update = useUpdateChannel();
  const remove = useDeleteChannel();
  const test = useTestChannel();

  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<string | null>(null);
  const [testResult, setTestResult] = useState<{ name: string; ok: boolean; message?: string } | null>(null);

  if (isLoading) return <CenteredSpinner />;
  const channels = data?.channels ?? [];
  const busy = create.isPending || update.isPending || remove.isPending;

  const runTest = async (name: string) => {
    setTestResult(null);
    try {
      await test.mutateAsync(name);
      setTestResult({ name, ok: true });
    } catch (err) {
      setTestResult({
        name,
        ok: false,
        message: err instanceof ApiError ? err.message : "request failed",
      });
    }
  };

  return (
    <Card className="overflow-hidden">
      <CardHeader>
        <CardTitle>Notification channels</CardTitle>
        {!adding && (
          <Button variant="secondary" size="sm" onClick={() => { setAdding(true); setEditing(null); }}>
            <Plus className="h-3.5 w-3.5" /> Add channel
          </Button>
        )}
      </CardHeader>
      {channels.length === 0 && !adding ? (
        <p className="px-4 pb-4 text-sm text-base-content/55">
          No channels yet. Add a webhook receiver so alert rules can deliver —
          rules reference channels by name (e.g. <span className="font-mono">p1</span>).
        </p>
      ) : (
        <ul className="divide-y divide-neutral">
          {channels.map((c) => (
            <li key={`${c.source}:${c.name}`} className="flex flex-col px-4 py-2.5 text-sm">
              <div className="flex items-center gap-2">
                <span className="font-mono font-medium">{c.name}</span>
                <Badge tone={c.source === "ui" ? "primary" : "neutral"}>
                  {c.source === "ui" ? "ui" : "config"}
                </Badge>
                {c.hasAuth && <span className="text-xs text-base-content/40">(signed)</span>}
                {c.shadowed && (
                  <Badge tone="warning" title="A UI channel with the same name wins at delivery time">
                    shadowed
                  </Badge>
                )}
                <span className="ml-auto flex items-center gap-1">
                  <Button variant="ghost" size="sm" onClick={() => void runTest(c.name)} disabled={test.isPending}>
                    <Send className="h-3.5 w-3.5" /> Test
                  </Button>
                  {c.source === "ui" && (
                    <>
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => { setEditing(c.name); setAdding(false); }}
                      >
                        <Pencil className="h-3.5 w-3.5" /> Edit
                      </Button>
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => remove.mutate(c.name)}
                        disabled={busy}
                      >
                        <Trash2 className="h-3.5 w-3.5" /> Delete
                      </Button>
                    </>
                  )}
                </span>
              </div>
              <span className="truncate text-xs text-base-content/55">{c.url}</span>
              {c.source === "config" && (
                <span className="text-xs text-base-content/40">edited in config (ConfigMap)</span>
              )}
              {testResult?.name === c.name && (
                <span className={testResult.ok ? "text-xs text-success" : "text-xs text-error"}>
                  {testResult.ok ? "test delivered" : `test failed: ${testResult.message}`}
                </span>
              )}
              {editing === c.name && c.source === "ui" && (
                <ChannelForm
                  initial={c}
                  busy={busy}
                  onCancel={() => setEditing(null)}
                  onSubmit={async (input) => {
                    await update.mutateAsync(input);
                    setEditing(null);
                  }}
                />
              )}
            </li>
          ))}
        </ul>
      )}
      {adding && (
        <ChannelForm
          busy={busy}
          onCancel={() => setAdding(false)}
          onSubmit={async (input) => {
            await create.mutateAsync(input);
            setAdding(false);
          }}
        />
      )}
    </Card>
  );
}
