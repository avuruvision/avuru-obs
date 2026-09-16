import { Pause, Play } from "lucide-react";
import { Button } from "@/components/ui/button";
import type { SceneSettings } from "./model";

export function XRayControls({ settings, reducedMotion, onChange }: { settings: SceneSettings; reducedMotion: boolean; onChange: (patch: Partial<SceneSettings>) => void }) {
  return <div className="flex flex-wrap items-center gap-x-6 gap-y-4 border-t border-neutral bg-base-200 px-5 py-4" aria-label="3D layer controls">
    <fieldset className="flex gap-3"><legend className="sr-only">Visible layers</legend>
      {([['pods', 'Pods'], ['nodes', 'Nodes'], ['infra', 'Logical infrastructure']] as const).map(([key, label]) =>
        <label key={key} className="flex items-center gap-2 text-xs"><input type="checkbox" checked={settings[key]} onChange={e => onChange({ [key]: e.target.checked })} className="accent-primary" />{label}</label>)}
    </fieldset>
    <label className="flex min-w-36 flex-1 items-center gap-3 text-xs">Spacing
      <input type="range" min="0" max="100" value={settings.spread} onChange={e => onChange({ spread: Number(e.target.value) })} className="w-20 flex-1 accent-primary" />
      <output className="w-8 text-right tabular-nums">{settings.spread}%</output>
    </label>
    <label className="flex min-w-44 flex-1 items-center gap-3 text-xs">X-Ray transparency
      <input type="range" min="0" max="90" value={settings.opacity} onChange={e => onChange({ opacity: Number(e.target.value) })} className="w-20 flex-1 accent-primary" />
      <output className="w-8 text-right tabular-nums">{settings.opacity}%</output>
    </label>
    <Button size="sm" disabled={reducedMotion} aria-pressed={settings.paused} onClick={() => onChange({ paused: !settings.paused })}>
      {settings.paused ? <Play className="h-3 w-3" /> : <Pause className="h-3 w-3" />}{settings.paused ? "Animate flows" : "Pause flows"}
    </Button>
  </div>;
}
