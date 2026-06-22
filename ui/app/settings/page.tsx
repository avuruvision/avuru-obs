import { Topbar } from "@/components/layout/topbar";
import { SettingsScreen } from "@/components/settings/settings-screen";

export default function SettingsPage() {
  return (
    <>
      <Topbar />
      <main className="flex-1 overflow-y-auto p-5">
        <SettingsScreen />
      </main>
    </>
  );
}
