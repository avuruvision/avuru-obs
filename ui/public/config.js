// Per-deployment UI config, injected at serve time (static export can't use
// runtime env vars). nginx/the chart can replace this file to point the SPA at
// a different API origin. Default "" = same-origin (`/api` proxied to the hub).
window.__AVURU_OBS_CONFIG__ = { apiBase: "" };
