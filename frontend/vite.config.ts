import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';
import { VitePWA } from 'vite-plugin-pwa';
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

// Kiln client is a mobile-first PWA (02 §11). The service worker + manifest make
// it installable and enable push/notification handling in later surface areas.
export default defineConfig({
  plugins: [
    react(),
    VitePWA({
      registerType: 'autoUpdate',
      // Self-destroying SW (recovery mechanism). The app is an online-only,
      // SSE-driven dashboard, and per the web-client spec the PWA/offline surface
      // is deferred to specs 09/10 — so precaching the app shell buys nothing here
      // and actively caused an outage: a previously-installed client kept serving
      // a stale precached `index.html` whose hashed CSS/JS no longer exist after a
      // deploy, so the page rendered unstyled. `selfDestroying` ships a worker that
      // unregisters itself and clears every cache on the client's next visit, after
      // which the app loads straight from the always-consistent (no-cache) server.
      // This auto-recovers already-installed clients with no manual clear. A real,
      // purpose-built service worker returns when notifications (09/10) need one.
      selfDestroying: true,
      workbox: {
        // Never precache source maps: they are upload-only artifacts (deleted
        // after upload) and must not be cached by — or shipped in — the SW.
        globIgnores: ['**/*.map'],
        // Don't emit maps for the generated SW (`sw.js`/`workbox-*.js`). When
        // `build.sourcemap` is on (token present), the PWA plugin runs after
        // Sentry's post-upload `.map` cleanup, so its maps would otherwise
        // survive in `dist`. We don't symbolicate the SW, so skip them.
        sourcemap: false,
      },
      manifest: {
        name: 'Kiln',
        short_name: 'Kiln',
        description: 'Voice-driven orchestrator for autonomous coding agents.',
        // Light paper surface (--surface-page, docs/ui/Kiln Colors.html).
        // `start_url` is `/`, so both the standalone system chrome
        // (`theme_color`) and the launch splash (`background_color`) read as
        // the app's own light surface rather than a dark value that
        // letterboxes the safe-area insets black. The manifest is static —
        // ThemeColorSync takes over at runtime for the dark theme.
        theme_color: '#faf6ef',
        background_color: '#faf6ef',
        display: 'standalone',
        orientation: 'portrait',
        start_url: '/',
      },
    }),
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
      '/auth': { target: process.env.KILN_PROXY_TARGET ?? 'http://localhost:8080', changeOrigin: true },
    },
  },
  test: {
    globals: true,
    environment: 'jsdom',
    setupFiles: ['./vitest.setup.ts'],
    css: true,
  },
});
