import type { JSX } from 'react';

// The app's crash fallback, rendered by the top-level Sentry `ErrorBoundary`
// (main.tsx) when the React tree throws (spec-10 §3). Minimal and on-brand: the
// Kiln dark palette from App.css, expressed with inline styles so it stays
// self-contained — it must render even when the app's CSS or a route chunk is
// what failed. `resetError` re-mounts the tree; the reload button is the sturdier
// escape hatch for a session that stays broken.
export function AppErrorFallback({ resetError }: { resetError: () => void }): JSX.Element {
  return (
    <div
      role="alert"
      style={{
        minHeight: '100dvh',
        display: 'flex',
        flexDirection: 'column',
        alignItems: 'center',
        justifyContent: 'center',
        gap: '1rem',
        padding: '1.5rem',
        textAlign: 'center',
        background: '#0b0b0f',
        color: '#e8e8ef',
        fontFamily: 'system-ui, -apple-system, "Segoe UI", Roboto, sans-serif',
      }}
    >
      <h1 style={{ margin: 0, fontSize: '1.25rem' }}>Something went wrong</h1>
      <p style={{ margin: 0, color: '#9a9aab', maxWidth: '32ch' }}>
        Kiln hit an unexpected error. Reloading usually clears it.
      </p>
      <button
        type="button"
        onClick={() => {
          // Try an in-place recovery first; a full reload is the fallback for a
          // session that stays broken.
          resetError();
          window.location.reload();
        }}
        style={{
          padding: '0.6rem 1.1rem',
          borderRadius: '0.5rem',
          border: '1px solid #2c2c38',
          background: '#5b8cff',
          color: '#0b0b0f',
          fontSize: '0.95rem',
          fontWeight: 600,
          cursor: 'pointer',
        }}
      >
        Reload
      </button>
    </div>
  );
}
