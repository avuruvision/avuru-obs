"use client";

import { useEffect, useRef, useState } from "react";
import { Focus, Minus, Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { SceneSettings, XRayModel } from "./model";
import type { CameraPose, createRenderer } from "./renderer";

export function XRayCanvas({ model, settings, onSelect, onInventory }: {
  model: XRayModel; settings: SceneSettings; onSelect: (id: string) => void; onInventory: () => void;
}) {
  const host = useRef<HTMLDivElement>(null);
  const engine = useRef<ReturnType<typeof createRenderer> | null>(null);
  const pose = useRef<CameraPose | undefined>(undefined);
  const latest = useRef({ settings, onSelect });
  const [status, setStatus] = useState<"loading" | "ready" | "failed">("loading");
  useEffect(() => { latest.current = { settings, onSelect }; engine.current?.update(settings); }, [settings, onSelect]);
  useEffect(() => {
    let cancelled = false;
    import("./renderer").then(({ createRenderer }) => {
      if (cancelled || !host.current) return;
      try {
        engine.current = createRenderer(host.current, model, latest.current.settings,
          id => latest.current.onSelect(id), () => setStatus("failed"), pose.current);
        setStatus("ready");
      } catch { setStatus("failed"); }
    }).catch(() => { if (!cancelled) setStatus("failed"); });
    return () => { cancelled = true; if (engine.current) pose.current = engine.current.dispose(); engine.current = null; };
  }, [model]);
  return (
    <div className="xray-viewport" data-testid="xray-viewport">
      <div ref={host} className="absolute inset-0" />
      <div className="pointer-events-none absolute left-5 top-5 z-10 text-[10px] tracking-widest text-base-content/80">
        CLUSTER X-RAY <span className="text-muted">/ LOGICAL PLACEMENT</span>
        <p className="mt-2 tracking-normal text-base-content/65">{model.nodes.length} nodes · {model.pods.length} pods · {model.flows.length} connections</p>
      </div>
      {status !== "ready" && <div role="status" className="absolute inset-0 z-20 flex flex-col items-center justify-center gap-4 bg-base-100/95 p-8 text-center">
        <p>{status === "failed" ? "3D is unavailable in this browser. Your inventory is still available." : "Preparing the 3D view…"}</p>
        {status === "failed" && <Button onClick={onInventory}>Open inventory</Button>}
      </div>}
      <div className="absolute right-4 top-4 z-10 flex flex-col rounded-lg border border-neutral bg-base-200 p-1">
        <Button size="icon" variant="ghost" aria-label="Zoom in" onClick={() => engine.current?.zoom(1.2)} disabled={status !== "ready"}><Plus className="h-4 w-4" /></Button>
        <Button size="icon" variant="ghost" aria-label="Zoom out" onClick={() => engine.current?.zoom(1 / 1.2)} disabled={status !== "ready"}><Minus className="h-4 w-4" /></Button>
        <Button size="icon" variant="ghost" aria-label="Reset camera" onClick={() => engine.current?.reset()} disabled={status !== "ready"}><Focus className="h-4 w-4" /></Button>
      </div>
      <p className="pointer-events-none absolute inset-x-0 bottom-4 text-center text-[10px] text-base-content/65">Drag to orbit · Scroll to zoom · Select a Pod to inspect</p>
    </div>
  );
}
