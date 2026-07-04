import '@testing-library/jest-dom/vitest';
import { vi } from 'vitest';

// jsdom does not implement `EventSource` (used by the real transport module
// for `GET /api/stream`, `src/transport/transport.ts`). Tests that exercise
// the stream directly install their own fake and drive its lifecycle
// (`src/transport/transport.test.ts`, via `vi.stubGlobal`); every other test
// that renders `<App />` (and so indirectly opens a stream through
// `BoardProvider`/`ChatProvider`) still constructs *some* `EventSource`, so
// this default keeps them from crashing with `ReferenceError: EventSource is
// not defined` — an inert connection that never opens or emits anything.
class InertEventSource {
  onopen: (() => void) | null = null;
  onerror: (() => void) | null = null;

  // No test relying on this default fallback asserts on live stream
  // behavior — those install their own fake instead — so the constructor
  // just needs to accept the URL argument (`new EventSource(url)`) and do
  // nothing with it.

  addEventListener(): void {
    // Inert: never fires.
  }

  removeEventListener(): void {
    // Inert: never fires.
  }

  close(): void {
    // Inert: nothing to tear down.
  }
}

vi.stubGlobal('EventSource', InertEventSource);
