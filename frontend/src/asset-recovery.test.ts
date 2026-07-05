import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { installAssetRecovery, isHashedAssetUrl, RELOAD_GUARD_KEY } from './asset-recovery';
// The shell's inline recovery bootstrap, imported as raw text so the parity test
// below can assert it stays in sync with this module.
import indexHtml from '../index.html?raw';

// A minimal in-memory Storage so each test starts from a clean guard and never
// touches the real (shared) sessionStorage.
function fakeStorage(): Storage {
  const map = new Map<string, string>();
  return {
    get length() {
      return map.size;
    },
    clear: () => {
      map.clear();
    },
    getItem: (k) => map.get(k) ?? null,
    key: (i) => Array.from(map.keys())[i] ?? null,
    removeItem: (k) => {
      map.delete(k);
    },
    setItem: (k, v) => {
      map.set(k, v);
    },
  };
}

// Dispatches a resource-load `error` event as the browser would: on the element,
// non-bubbling, so it only reaches the window's capture-phase listener.
function dispatchAssetError(el: Element): void {
  el.dispatchEvent(new Event('error', { bubbles: false }));
}

describe('isHashedAssetUrl', () => {
  const origin = 'http://localhost:3000';

  it('matches same-origin /assets/ bundles', () => {
    expect(isHashedAssetUrl(`${origin}/assets/index-ABCD1234.css`, origin)).toBe(true);
    expect(isHashedAssetUrl(`${origin}/assets/index-ABCD1234.js`, origin)).toBe(true);
    expect(isHashedAssetUrl('/assets/chunk-DEADBEEF.js', origin)).toBe(true);
  });

  it('ignores non-asset paths and cross-origin resources', () => {
    // The Google Fonts stylesheet the shell loads — a failure there must never
    // reload the whole app.
    expect(isHashedAssetUrl('https://fonts.googleapis.com/css2?family=X', origin)).toBe(false);
    expect(isHashedAssetUrl(`${origin}/favicon.ico`, origin)).toBe(false);
    expect(isHashedAssetUrl(`${origin}/api/board`, origin)).toBe(false);
    expect(isHashedAssetUrl('not a url', origin)).toBe(false);
  });
});

describe('installAssetRecovery', () => {
  let teardown: (() => void) | undefined;
  let reload: ReturnType<typeof vi.fn>;
  let storage: Storage;
  let clock: { now: number };

  beforeEach(() => {
    reload = vi.fn();
    storage = fakeStorage();
    clock = { now: 1_000_000 };
  });

  afterEach(() => {
    teardown?.();
    document.head.querySelectorAll('link,script').forEach((el) => {
      el.remove();
    });
  });

  function install(minIntervalMs = 10_000) {
    teardown = installAssetRecovery({
      reload,
      storage,
      now: () => clock.now,
      minIntervalMs,
    });
  }

  // The core deliverable: a client holding the previous deploy's shell requests a
  // hashed asset that the new deploy no longer has. The 404 surfaces as a resource
  // `error` event; recovery must reload the tab exactly once onto the current shell.
  it('reloads once when a stale hashed asset (prior deploy) fails to load', () => {
    install();
    const link = document.createElement('link');
    link.rel = 'stylesheet';
    link.href = '/assets/index-OLDHASH0.css'; // hash from the superseded deploy
    document.head.appendChild(link);

    dispatchAssetError(link);

    expect(reload).toHaveBeenCalledTimes(1);
    expect(storage.getItem(RELOAD_GUARD_KEY)).toBe(String(clock.now));
  });

  it('reloads for a failed entry/module script as well as a stylesheet', () => {
    install();
    const script = document.createElement('script');
    script.src = '/assets/index-OLDHASH0.js';
    document.head.appendChild(script);

    dispatchAssetError(script);

    expect(reload).toHaveBeenCalledTimes(1);
  });

  // Loop guard: after one recovery reload the tab lands on a fresh shell. If its
  // assets *also* 404 immediately (a genuinely broken deploy), reloading again
  // won't help — we must stop rather than thrash.
  it('does not reload again within the guard window (no reload storm)', () => {
    install(10_000);
    const link = document.createElement('link');
    link.href = '/assets/index-OLDHASH0.css';
    document.head.appendChild(link);

    dispatchAssetError(link);
    expect(reload).toHaveBeenCalledTimes(1);

    // A second failure 3s later (simulating the reloaded-but-still-broken shell).
    clock.now += 3_000;
    dispatchAssetError(link);
    expect(reload).toHaveBeenCalledTimes(1); // still just one — storm suppressed
  });

  it('reloads again once the guard window has elapsed (a later, separate deploy)', () => {
    install(10_000);
    const link = document.createElement('link');
    link.href = '/assets/index-OLDHASH0.css';
    document.head.appendChild(link);

    dispatchAssetError(link);
    expect(reload).toHaveBeenCalledTimes(1);

    clock.now += 11_000; // past the guard window
    dispatchAssetError(link);
    expect(reload).toHaveBeenCalledTimes(2);
  });

  it('a clean full load resets the guard so a later strand can recover', () => {
    install(10_000);
    const link = document.createElement('link');
    link.href = '/assets/index-OLDHASH0.css';
    document.head.appendChild(link);

    dispatchAssetError(link);
    expect(reload).toHaveBeenCalledTimes(1);

    // The tab reloaded and everything loaded fine: `load` fires and clears the
    // guard. A strand from a *future* deploy (still inside the raw time window)
    // must then be free to recover again.
    window.dispatchEvent(new Event('load'));
    expect(storage.getItem(RELOAD_GUARD_KEY)).toBeNull();

    clock.now += 1_000; // well within the guard window
    dispatchAssetError(link);
    expect(reload).toHaveBeenCalledTimes(2);
  });

  it('ignores load failures for non-asset resources (fonts, images, favicon)', () => {
    install();
    const font = document.createElement('link');
    font.rel = 'stylesheet';
    font.href = 'https://fonts.googleapis.com/css2?family=Space+Grotesk';
    document.head.appendChild(font);
    const favicon = document.createElement('link');
    favicon.rel = 'icon';
    favicon.href = '/favicon.ico';
    document.head.appendChild(favicon);

    dispatchAssetError(font);
    dispatchAssetError(favicon);

    expect(reload).not.toHaveBeenCalled();
  });

  it('recovers when a Vite dynamic-import chunk fails (long-lived tab after deploy)', () => {
    install();
    const event = new Event('vite:preloadError', { cancelable: true });
    window.dispatchEvent(event);

    expect(reload).toHaveBeenCalledTimes(1);
    expect(event.defaultPrevented).toBe(true); // suppresses the unhandled throw
  });

  it('stops listening after teardown', () => {
    install();
    teardown?.();
    const link = document.createElement('link');
    link.href = '/assets/index-OLDHASH0.css';
    document.head.appendChild(link);

    dispatchAssetError(link);

    expect(reload).not.toHaveBeenCalled();
  });
});

// Drift guard: the inline bootstrap in index.html (the only recovery that runs
// when the entry module itself 404s) must share the exact sessionStorage key the
// module uses, or the two could reload-storm each other across a navigation.
describe('inline bootstrap parity', () => {
  it('index.html embeds the same reload-guard key as the module', () => {
    expect(indexHtml).toContain(RELOAD_GUARD_KEY);
    // ...and the same guard window, so their loop-suppression stays consistent.
    expect(indexHtml).toContain('10000');
  });
});
