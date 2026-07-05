// The dock (08 §2 dock region, 09 §3–§4): the mic button + live transcript + the
// cancel (X) + the send button. Presentational consumer of the voice store — all
// lifecycle and commit logic lives in `voice-store`/`commit-machine`; this file
// only renders `useVoice()`'s `{ micState, settledText, tailText }` and forwards
// taps to `pause`/`resume`/`cancel`/`sendNow`.
//
// The 08 §F selector surface is preserved verbatim (`data-role="dock"`,
// `"dock-talk"`, `aria-label="Talk"`, and the mic-glyph sub-elements) so
// `PrimaryScreen.css` and existing selectors keep working; `data-dock-state` now
// reflects the live `micState` instead of the placeholder `"idle"`.
import { useEffect, useRef, type JSX } from 'react';
import { useVoice } from '@/voice/voice-context';
import type { MicState } from '@/voice/commit-machine';

// The mic-button copy per state (09 §3 table). Listening is the amber resting
// state; the rest are grey and tap-to-act.
const LABELS: Record<MicState, string> = {
  listening: 'Listening…',
  paused: 'Tap to talk',
  denied: 'Tap to enable mic',
  retry: 'Tap to retry',
};

export function Dock(): JSX.Element {
  const { micState, settledText, tailText, pause, resume, cancel, sendNow } = useVoice();
  const transcriptRef = useRef<HTMLDivElement | null>(null);

  // One mic tap: pause while listening, otherwise resume/retry (09 §3, §5). This
  // only stops/starts the mic — it does NOT send (use the send button for that).
  // Any transcript stays on screen while paused so it can still be sent or cleared.
  const onMicTap = (): void => {
    if (micState === 'listening') {
      pause();
    } else {
      resume();
    }
  };

  // Both side controls appear whenever there is any transcript text on screen —
  // interim or settled, listening or paused (the "stuck" case) — so the user can
  // always send the shown transcript (send button) or clear it (X). Neither is
  // gated on listening state or on a final having landed (09 §4).
  const hasTranscript = settledText !== '' || tailText !== '';
  const showSend = hasTranscript;
  const showCancel = hasTranscript;

  // Keep the notification hub clear of the dock as the transcript grows (08 §4 /
  // the bottom-anchored-UI layering principle — see the web-client skill). The
  // toast overlay and the live transcript both grow UPWARD from the dock's top
  // edge, so on a shared baseline they would overlap. Publish the transcript's
  // current height as `--dock-overlay-height` on the screen root; the activity
  // row offsets its `bottom` by that value and so always floats above the
  // transcript — collapsed (var unset → 0px) or expanded, tracked live as words
  // stream in via ResizeObserver. Written on the screen root (not the dock) so it
  // reaches the activity row, which is the dock's sibling; a no-op when the dock
  // renders outside a primary screen (isolated tests) since `closest` is null.
  useEffect(() => {
    const el = transcriptRef.current;
    const root = el?.closest<HTMLElement>('[data-role="primary-screen"]') ?? null;
    if (root === null) {
      return;
    }
    const publish = (): void => {
      root.style.setProperty('--dock-overlay-height', `${(el?.offsetHeight ?? 0).toString()}px`);
    };
    publish();
    if (el === null || typeof ResizeObserver === 'undefined') {
      return () => {
        root.style.removeProperty('--dock-overlay-height');
      };
    }
    const observer = new ResizeObserver(publish);
    observer.observe(el);
    return () => {
      observer.disconnect();
      root.style.removeProperty('--dock-overlay-height');
    };
  }, [hasTranscript]);

  return (
    <div data-role="dock" data-dock-state={micState}>
      {hasTranscript && (
        <div data-role="dock-transcript" data-dock-state={micState} ref={transcriptRef}>
          {settledText !== '' && <span data-role="dock-settled">{settledText}</span>}
          {tailText !== '' && (
            <span data-role="dock-tail" data-ghost="true">
              {tailText}
            </span>
          )}
          {micState === 'listening' && (
            <span data-role="dock-caret" aria-hidden="true">
              |
            </span>
          )}
        </div>
      )}

      <div data-role="dock-controls">
        {showSend && (
          <button type="button" data-role="dock-send" aria-label="Send" onClick={sendNow}>
            <svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true">
              <path d="M12 4l-8 8h5v8h6v-8h5z" fill="currentColor" />
            </svg>
          </button>
        )}

        <button
          type="button"
          data-role="dock-talk"
          data-dock-state={micState}
          aria-label="Talk"
          aria-pressed={micState === 'listening'}
          onClick={onMicTap}
        >
          <span data-role="dock-mic" aria-hidden="true">
            <span data-role="dock-mic-orb" />
            <span data-role="dock-mic-capsule" />
            <span data-role="dock-mic-arc" />
            <span data-role="dock-mic-stem" />
          </span>
          <span data-role="dock-label">{LABELS[micState]}</span>
        </button>

        {showCancel && (
          <button type="button" data-role="dock-cancel" aria-label="Cancel" onClick={cancel}>
            <span aria-hidden="true">×</span>
          </button>
        )}
      </div>
    </div>
  );
}
