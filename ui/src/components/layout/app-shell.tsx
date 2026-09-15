"use client";

import { createContext, useContext, useEffect, useRef, useState } from "react";
import { Menu, X } from "lucide-react";
import { Sidebar } from "./sidebar";

const NavigationContext = createContext<() => void>(() => {});

export function MobileNavigationButton() {
  const open = useContext(NavigationContext);
  return (
    <button type="button" onClick={open} aria-label="Open navigation"
      className="rounded-md border border-neutral p-2 md:hidden">
      <Menu className="h-4 w-4" aria-hidden />
    </button>
  );
}

function MobileNavigation({ onClose }: { onClose: () => void }) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const dialog = ref.current;
    dialog?.showModal();
    return () => dialog?.close();
  }, []);
  return (
    <dialog ref={ref} aria-label="Navigation" onClose={onClose}
      className="fixed inset-y-0 left-0 m-0 h-dvh max-h-none w-64 max-w-[90vw] border-r border-neutral bg-base-200 p-0 text-base-content backdrop:bg-black/40">
      <button type="button" aria-label="Close navigation" onClick={onClose}
        className="absolute right-2 top-4 z-10 rounded-md bg-base-200 p-2">
        <X className="h-4 w-4" aria-hidden />
      </button>
      <div onClick={(event) => {
        if ((event.target as Element).closest("a[href]")) onClose();
      }}>
        <Sidebar mobile />
      </div>
    </dialog>
  );
}

export function AppShell({ children }: { children: React.ReactNode }) {
  const [mobileOpen, setMobileOpen] = useState(false);
  return (
    <NavigationContext.Provider value={() => setMobileOpen(true)}>
      <div className="flex h-dvh overflow-hidden">
        <div className="hidden md:block"><Sidebar /></div>
        {mobileOpen && <MobileNavigation onClose={() => setMobileOpen(false)} />}
        <div className="flex min-w-0 flex-1 flex-col">{children}</div>
      </div>
    </NavigationContext.Provider>
  );
}
