import type { NextConfig } from "next";

// Static export: the UI ships as static assets served by its own nginx image
// (separate deployable), single-origin with the hub. See agent_docs/ui_patterns.md.
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
