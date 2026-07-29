"use client";

import { Card, CardHeader, CardTitle } from "@/components/ui/card";
import { ProjectSettingsCard } from "@/components/settings/project-settings-card";
import { useSystemStatus } from "@/hooks/use-system-status";

// Coroot-style General settings: the active project (admins create/rename/delete
// db-managed projects; default/config stay read-only) and this install's
// retention.
export function GeneralTab() {
  const { data: status } = useSystemStatus();

  return (
    <div className="flex flex-col gap-4">
      <ProjectSettingsCard />

      {status && (
        <Card className="overflow-hidden">
          <CardHeader>
            <CardTitle>Retention</CardTitle>
            <span className="text-xs text-base-content/50">
              per-signal TTL, instance-wide
            </span>
          </CardHeader>
          <div className="grid gap-px border-t border-neutral bg-neutral sm:grid-cols-4">
            {status.signals.map((s) => (
              <div key={s.signal} className="bg-base-200 p-3">
                <p className="text-xs uppercase tracking-wider text-base-content/50">
                  {s.signal}
                </p>
                <p className="text-sm font-semibold">{s.retentionDays} days</p>
              </div>
            ))}
          </div>
        </Card>
      )}
    </div>
  );
}
