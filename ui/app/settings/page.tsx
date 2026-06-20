import { Settings } from "lucide-react";
import { Topbar } from "@/components/layout/topbar";
import { ComingSoon } from "@/components/ui/coming-soon";

export default function SettingsPage() {
  return (
    <>
      <Topbar />
      <main className="flex-1 overflow-y-auto p-5">
        <ComingSoon icon={Settings} title="Settings" milestone="M5">
          Authentication, retention policies, agent configuration (OpAMP) —
          the control plane gets its controls in the hardening milestone.
        </ComingSoon>
      </main>
    </>
  );
}
