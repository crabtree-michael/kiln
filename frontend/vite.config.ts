import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';
import { sentryVitePlugin } from '@sentry/vite-plugin';

// This config runs in Node (build-time), not the browser, so `process` exists at
// runtime. We declare just the slice we read rather than pulling @types/node —
// which would leak Node globals into the browser `src` the tsconfig keeps out.
// File-scoped (this module has imports), so it does not leak elsewhere.
declare const process: { env: Record<string, string | undefined> };

// Sentry source-map upload (spec-10 §3) is gated on the org auth token: baked
// in at Docker build time, absent for local `pnpm dev`/`pnpm build`/tests. With
// no token we neither run the plugin nor emit `.map` files — so a plain local
// build is byte-for-byte what it was before this integration.
const sentryAuthToken = process.env.SENTRY_AUTH_TOKEN;
const uploadSourceMaps = sentryAuthToken !== undefined && sentryAuthToken.length > 0;
const sentryPlugins =
  sentryAuthToken !== undefined && sentryAuthToken.length > 0
    ? [
        sentryVitePlugin({
          org: 'kiln-5p',
          project: 'kiln-frontend',
          // US-region orgs are served from sentry.io (the token's embedded host);
          // forcing us.sentry.io here makes sentry-cli 401 on upload.
          url: 'https://sentry.io',
          authToken: sentryAuthToken,
          sourcemaps: {
            // Symbolication maps are upload-only: delete them once Sentry has
            // them so no `.map` ever ships in `dist`.
            filesToDeleteAfterUpload: ['./dist/**/*.map'],
          },
        }),
      ]
    : [];

// Kiln client is a mobile-first PWA (02 §11). Notifications (02 §10) need a real
// service worker, which now ships as a static, purpose-built `public/push-sw.js`
// (push + notificationclick only, PRECACHES NOTHING) registered on opt-in — see
// that file and `src/stores/use-web-push.ts`. It replaces the former
// vite-plugin-pwa self-destroying worker: a hand-written static worker is served
// verbatim and can never precache a stale app shell (the outage that motivated
// selfDestroying), so we no longer need the plugin. The web app manifest is now a
// static `public/manifest.webmanifest` linked from index.html.
export default defineConfig({
  plugins: [
    react(),
    // Sentry goes last (per its docs) and only when a token is present; empty
    // otherwise, so local builds are untouched.
    ...sentryPlugins,
  ],
  build: {
    // Emit source maps only when we will upload them to Sentry — without a
    // token none are generated, so none can leak into `dist`.
    sourcemap: uploadSourceMaps,
  },
  resolve: {
    alias: { '@': new URL('./src', import.meta.url).pathname },
  },
  server: {
    host: true,
    port: 5173,
    // The live connection and HTTP API are served by the backend (02 §7). The
    // client talks same-origin (`/api/...`, see transport.ts) and the dev server
    // proxies to the backend. Target is env-driven because the backend's address
    // differs by environment: `localhost:8080` for a bare `pnpm dev`, but
    // `http://backend:8080` (the compose service name) when the frontend runs in
    // its own container — inside that container `localhost` is not the backend.
    proxy: {
      '/api': {
        target: process.env.KILN_PROXY_TARGET ?? 'http://localhost:8080',
        changeOrigin: true,
        ws: true,
      },
      '/auth': {
        target: process.env.KILN_PROXY_TARGET ?? 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
  test: {
    globals: true,
    environment: 'jsdom',
    setupFiles: ['./vitest.setup.ts'],
    css: true,
  },
});
