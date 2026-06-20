import { cva, type VariantProps } from "class-variance-authority";
import { cn } from "@/lib/cn";

const badgeVariants = cva(
  "inline-flex items-center rounded-md px-1.5 py-0.5 font-mono text-xs font-medium",
  {
    variants: {
      tone: {
        neutral: "bg-base-300 text-base-content/70",
        success: "bg-success/15 text-success",
        error: "bg-error/15 text-error",
        warning: "bg-warning/15 text-warning",
        primary: "bg-primary/15 text-primary",
      },
    },
    defaultVariants: { tone: "neutral" },
  },
);

export interface BadgeProps
  extends React.HTMLAttributes<HTMLSpanElement>,
    VariantProps<typeof badgeVariants> {}

export function Badge({ className, tone, ...props }: BadgeProps) {
  return <span className={cn(badgeVariants({ tone }), className)} {...props} />;
}
