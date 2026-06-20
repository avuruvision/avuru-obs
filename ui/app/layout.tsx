import type { Metadata } from "next";
import { Inter } from "next/font/google";
import Script from "next/script";
import { Providers } from "@/components/layout/providers";
import { AppShell } from "@/components/layout/app-shell";
import "./globals.css";

const inter = Inter({ subsets: ["latin"], variable: "--font-inter" });

export const metadata: Metadata = {
  title: "Avuru Obs",
  description:
    "All-in-one observability: traces, metrics, logs, profiling — live in 5 minutes.",
};

export default function RootLayout({
  children,
}: Readonly<{ children: React.ReactNode }>) {
  return (
    // suppressHydrationWarning: next-themes mutates data-theme on <html>
    // before hydration (agent_docs/ui_patterns.md).
    <html lang="en" suppressHydrationWarning className={inter.variable}>
      <body className="antialiased">
        {/* Per-deployment API base, injected before hydration (static export
            forbids runtime env vars). nginx/the chart can swap /config.js. */}
        <Script src="/config.js" strategy="beforeInteractive" />
        <Providers>
          <AppShell>{children}</AppShell>
        </Providers>
      </body>
    </html>
  );
}
