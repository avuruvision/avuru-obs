import { Topbar } from "@/components/layout/topbar";
import { UsersPanel } from "@/components/settings/users-panel";

// Admin-only user administration. Reachable from the Settings tab bar (shown
// only to admins) or directly; a non-admin who lands here sees the panel's
// load error since the hub answers 403 on /api/v1/users.
export default function UsersPage() {
  return (
    <>
      <Topbar />
      <main className="flex-1 overflow-y-auto p-5">
        <UsersPanel />
      </main>
    </>
  );
}
