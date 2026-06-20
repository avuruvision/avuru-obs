import type { NextConfig } from "next";

// Static export is a hard constraint: the UI is embedded into the hub Go
// binary (go:embed). See agent_docs/ui_patterns.md before changing anything.
const isDev = process.env.NODE_ENV === "development";

const nextConfig: NextConfig = {
  ...(isDev
    ? {
        // Dev-only: proxy API calls to a locally running hub.
        async rewrites() {
          return [
            {
              source: "/api/:path*",
              destination: "http://localhost:8080/api/:path*",
            },
          ];
        },
      }
    : { output: "export" }),
  images: { unoptimized: true },
};

export default nextConfig;
