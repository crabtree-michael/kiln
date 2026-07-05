// Client-side deploy-rollover recovery.
//
// Kiln ships as a single Go binary with the built frontend embedded, and every
// deploy replaces the whole hashed-asset set with a disjoint one (new content
// hashes -> new filenames). The moment a new deploy goes live, every asset URL
// from the previous build 404s (the server answers an honest 404 rather than the
// HTML shell — see backend/internal/web/embed.go). A client still holding the
// previous build's shell — a stale HTTP-cache entry, a backgrounded tab, a
// long-lived session that later pulls a dynamic chunk — then requests a hash that
// no longer exists and renders unstyled (missing CSS) or blank (missing entry
// JS). There is no service worker to recover it (the PWA SW is self-destroying,
// deferred to specs 09/10).
//
// This module makes such a client self-heal: it listens for a hashed-asset load
// failure and forces one full reload. The reload re-fetches the shell, which the
// server serves `no-cache`, so the client lands on the *current* deploy's shell
// referencing assets that actually resolve — closing the rollover gap without any
// cross-deploy asset retention (impossible with an embedded, immutable binary).
//
// A minimal inline copy of the same reload lives in index.html to cover the case
// where the entry module itself 404s (this file never runs then); this module is
// the tested, richer implementation that also catches Vite dynamic-import
// ("preload") failures in a long-lived tab. Both coordinate through the same
// sessionStorage guard so they can never reload-storm each other.

// Shared across the inline bootstrap (index.html) and this module: the timestamp
// of the last recovery reload, so a genuinely broken deploy (fresh shell whose
// assets also 404) fails visibly instead of thrashing the tab.
export const RELOAD_GUARD_KEY = 'kiln:asset-recovery-reloaded-at';

// Minimum gap between two recovery reloads. One stale-shell mismatch clears in a
// single reload, so a second failure inside this window means reloading again
// won't help — stop and let it fail visibly.
const DEFAULT_MIN_INTERVAL_MS = 10_000;

export interface AssetRecoveryOptions {
  /** Window to attach listeners to and reload. Defaults to the global window. */
  win?: Window & typeof globalThis;
  /** Forces the reload. Defaults to `win.location.reload()`. */
  reload?: () => void;
  /** Clock, injectable for tests. Defaults to `Date.now`. */
  now?: () => number;
  /**
   * Cross-reload guard store. Defaults to `win.sessionStorage` (falling back to
   * in-memory only when it throws, e.g. private mode). Pass `null` to force the
   * in-memory fallback in tests.
   */
  storage?: Storage | null;
  /** Minimum gap between recovery reloads. Defaults to 10s. */
  minIntervalMs?: number;
}

/**
 * Reports whether `url` is one of our content-hashed bundle assets — a
 * same-origin resource under `/assets/`. Vite emits every hashed JS/CSS chunk
 * there, so a load failure for such a URL is a superseded-deploy strand, not a
 * broken third-party resource (fonts, images) we should never reload for.
 */
export function isHashedAssetUrl(url: string, origin: string): boolean {
  try {
    const parsed = new URL(url, origin);
    return parsed.origin === origin && parsed.pathname.startsWith('/assets/');
  } catch {
    return false;
  }
}

/**
 * Installs the recovery listeners and returns a teardown function (used by tests;
 * production installs once for the page's lifetime). Idempotent per window is not
 * guaranteed — call it once.
 */
export function installAssetRecovery(options: AssetRecoveryOptions = {}): () => void {
  const win = options.win ?? window;
  const reload =
    options.reload ??
    (() => {
      win.location.reload();
    });
  const now = options.now ?? Date.now;
  const minInterval = options.minIntervalMs ?? DEFAULT_MIN_INTERVAL_MS;
  const storage = options.storage === undefined ? safeSessionStorage(win) : options.storage;

  // In-memory fallback so the loop guard still holds within a single page even
  // when persistent storage is unavailable (it resets across the reload, which is
  // the storage's job — best effort without it).
  let memoryGuard: number | null = null;

  const readGuard = (): number | null => {
    if (storage !== null) {
      try {
        const raw = storage.getItem(RELOAD_GUARD_KEY);
        if (raw !== null) return Number.parseInt(raw, 10);
      } catch {
        // fall through to memory
      }
    }
    return memoryGuard;
  };

  const writeGuard = (at: number): void => {
    memoryGuard = at;
    if (storage !== null) {
      try {
        storage.setItem(RELOAD_GUARD_KEY, String(at));
      } catch {
        // best effort — memoryGuard still holds within this page
      }
    }
  };

  const clearGuard = (): void => {
    memoryGuard = null;
    if (storage !== null) {
      try {
        storage.removeItem(RELOAD_GUARD_KEY);
      } catch {
        // ignore
      }
    }
  };

  const recover = (): boolean => {
    const last = readGuard();
    if (last !== null && !Number.isNaN(last) && now() - last < minInterval) {
      // Already reloaded moments ago and the assets still 404: the current deploy
      // is itself broken, not a stale-cache mismatch. Reloading again won't help,
      // so stop rather than thrash the tab.
      return false;
    }
    writeGuard(now());
    reload();
    return true;
  };

  const onError = (event: Event): void => {
    const el = event.target;
    let url: string | null = null;
    if (el instanceof win.HTMLLinkElement) url = el.href;
    else if (el instanceof win.HTMLScriptElement) url = el.src;
    if (url !== null && url !== '' && isHashedAssetUrl(url, win.location.origin)) {
      recover();
    }
  };

  // Vite raises `vite:preloadError` when a dynamically imported chunk fails to
  // load — e.g. a lazily loaded route or an audio worklet pulled after a deploy
  // in a tab that stayed open. Preventing the default stops the unhandled throw;
  // the reload lands the tab on the current build.
  const onPreloadError = (event: Event): void => {
    event.preventDefault();
    recover();
  };

  // A clean, fully successful load means every asset resolved — reset the guard
  // so a future deploy's strand is free to trigger a fresh recovery reload.
  const onLoad = (): void => {
    clearGuard();
  };

  // Capture phase: resource-load `error` events (from <link>/<script>) do not
  // bubble, but they do propagate to window in the capture phase.
  win.addEventListener('error', onError, true);
  win.addEventListener('vite:preloadError', onPreloadError);
  win.addEventListener('load', onLoad);

  return () => {
    win.removeEventListener('error', onError, true);
    win.removeEventListener('vite:preloadError', onPreloadError);
    win.removeEventListener('load', onLoad);
  };
}

// safeSessionStorage returns sessionStorage, or null if touching it throws
// (Safari private mode, disabled storage). Probing at install time keeps the hot
// path free of try/catch on every read.
function safeSessionStorage(win: Window & typeof globalThis): Storage | null {
  try {
    return win.sessionStorage;
  } catch {
    return null;
  }
}
