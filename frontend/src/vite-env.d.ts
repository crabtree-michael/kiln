/// <reference types="vite/client" />

// Build-time env the client reads via `import.meta.env`. These merge into
// Vite's own `ImportMetaEnv` (from `vite/client`, already in tsconfig `types`),
// so `import.meta.env.MODE`/`DEV`/… stay typed alongside our additions.
interface ImportMetaEnv {
  /**
   * Sentry DSN for the `kiln-frontend` project, baked in at build time
   * (spec-10 §3). A public value, not a secret. Undefined/empty in local
   * `pnpm dev`, plain builds, and the test run — Sentry stays a no-op unless
   * this is set, so nothing changes locally.
   */
  readonly VITE_SENTRY_DSN?: string;
  /**
   * Optional release identifier (git SHA / `VERSION`) tagged on Sentry events
   * so errors and traces group per deploy. Omitted → Sentry auto-detects/none.
   */
  readonly VITE_RELEASE?: string;
  /**
   * Set to `'1'` to re-expose the per-user Anthropic API key field in the
   * dashboard. The Anthropic key is a deployment-global `ANTHROPIC_API_KEY`
   * setting now, so onboarding no longer asks for it and the field is hidden by
   * default; this flag brings it back (with its still-intact commit/verify
   * path) without a code change, pending real per-user config support.
   */
  readonly VITE_SHOW_ANTHROPIC_KEY_FIELD?: string;
}

// The W3C Audio Session API (https://www.w3.org/TR/audio-session/). Not in the
// DOM lib types because it is not Baseline — only Safari 16.4+ implements it. We
// use it to declare a `play-and-record` session so iOS lets other apps' audio
// (music, podcasts) keep playing (ducked) while the mic is live, instead of
// interrupting it. `audioSession` is optional so feature-detection stays honest
// on every other browser, where it is simply absent (no polyfill, no-op).
type AudioSessionType =
  'auto' | 'playback' | 'transient' | 'transient-solo' | 'ambient' | 'play-and-record';

interface AudioSession {
  type: AudioSessionType;
}

interface Navigator {
  readonly audioSession?: AudioSession;
  /**
   * iOS Safari's non-standard flag: `true` only when the page is running as an
   * installed home-screen web app (standalone display), absent in a normal
   * browser tab. Not in the DOM lib types, and the only reliable "installed web
   * app" signal on iOS (which does not implement the `display-mode` media
   * query). Used to send installed iOS web-app launches straight to the app
   * instead of the marketing landing page. See src/standalone.ts.
   */
  readonly standalone?: boolean;
}
