import { Loader2 } from "lucide-react";
import { cn } from "@/lib/cn";

export function Spinner({ className }: { className?: string }) {
  return (
    <Loader2
      aria-label="Loading"
      className={cn("h-5 w-5 animate-spin text-base-content/40", className)}
    />
  );
}

export function CenteredSpinner() {
  return (
    <div className="flex min-h-32 items-center justify-center">
      <Spinner />
    </div>
  );
}
