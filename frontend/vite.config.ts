import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';
import { VitePWA } from 'vite-plugin-pwa';

// This config runs in Node (build-time), not the browser, so `process` exists at
// runtime. We declare just the slice we read rather than pulling @types/node —
// which would leak Node globals into the browser `src` the tsconfig keeps out.
// File-scoped (this module has imports), so it does not leak elsewhere.
declare const process: { env: Record<string, string | undefined> };

// Kiln client is a mobile-first PWA (02 §11). The service worker + manifest make
// it installable and enable push/notification handling in later surface areas.
export default defineConfig({
  plugins: [
    react(),
    VitePWA({
      registerType: 'autoUpdate',
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
  ],
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
