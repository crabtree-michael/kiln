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
}
