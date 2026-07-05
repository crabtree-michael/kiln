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
          org: 'macmail',
          project: 'kiln-frontend',
          url: 'https://de.sentry.io',
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
        theme_color: '#0b0b0f',
        background_color: '#0b0b0f',
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
    },
  },
  test: {
    globals: true,
    environment: 'jsdom',
    setupFiles: ['./vitest.setup.ts'],
    css: true,
  },
});
