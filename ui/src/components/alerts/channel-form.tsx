"use client";

import { useState } from "react";
import { Button } from "@/components/ui/button";
import { ApiError } from "@/lib/api";
import type { AlertChannel, AlertChannelInput } from "@/lib/api-types";

// Inline create/edit form for a webhook channel. The name is the identity
// (disabled on edit); an empty secret on edit keeps the stored one.
export function ChannelForm({
  initial,
  onSubmit,
  onCancel,
  busy,
}: {
  initial?: AlertChannel;
  onSubmit: (input: AlertChannelInput) => Promise<unknown>;
  onCancel: () => void;
  busy: boolean;
}) {
  const [name, setName] = useState(initial?.name ?? "");
  const [url, setUrl] = useState(initial?.url ?? "");
  const [secret, setSecret] = useState("");
  const [error, setError] = useState<string | null>(null);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    if (!/^https?:\/\/.+/.test(url)) {
      setError("URL must be http(s)");
      return;
    }
    try {
      await onSubmit({ name, type: "webhook", url, secret: secret || undefined });
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "request failed");
    }
  };

  const inputClass =
    "h-8 w-full rounded-lg border border-neutral bg-base-100 px-2.5 text-sm focus-visible:outline-2 focus-visible:outline-primary";

  return (
    <form onSubmit={submit} className="flex flex-col gap-2 border-t border-neutral px-4 py-3">
      <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
        <label className="flex flex-col gap-1 text-xs text-base-content/60">
          Name
          <input
            className={inputClass}
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="p1"
            disabled={!!initial}
            required
          />
        </label>
        <label className="flex flex-col gap-1 text-xs text-base-content/60">
          Webhook URL
          <input
            className={inputClass}
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            placeholder="https://hooks.example.com/…"
            required
          />
        </label>
      </div>
      <label className="flex flex-col gap-1 text-xs text-base-content/60">
        Signing secret{" "}
        {initial?.hasAuth && <span className="text-base-content/40">(leave blank to keep the current one)</span>}
        <input
          className={inputClass}
          type="password"
          value={secret}
          onChange={(e) => setSecret(e.target.value)}
          placeholder={initial?.hasAuth ? "••••••••" : "optional"}
        />
      </label>
      {error && <p className="text-xs text-error">{error}</p>}
      <div className="flex items-center gap-2">
        <Button type="submit" variant="primary" size="sm" disabled={busy}>
          {initial ? "Save" : "Add channel"}
        </Button>
        <Button type="button" variant="ghost" size="sm" onClick={onCancel} disabled={busy}>
          Cancel
        </Button>
      </div>
    </form>
  );
}
