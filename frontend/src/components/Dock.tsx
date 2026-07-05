// The dock (08 §2 dock region, 09 §3–§4): the mic button + live transcript + the
// cancel (X) + the send button. Presentational consumer of the voice store — all
// lifecycle and commit logic lives in `voice-store`/`commit-machine`; this file
// only renders `useVoice()`'s `{ micState, settledText, tailText, pendingSend }`
// and forwards taps to `pause`/`resume`/`cancel`/`sendNow`.
//
// The 08 §F selector surface is preserved verbatim (`data-role="dock"`,
// `"dock-talk"`, `aria-label="Talk"`, and the mic-glyph sub-elements) so
// `PrimaryScreen.css` and existing selectors keep working; `data-dock-state` now
// reflects the live `micState` instead of the placeholder `"idle"`.
import { useEffect, useRef, type JSX } from 'react';
import { useVoice } from '@/voice/voice-context';
import { VolumeSmoother, toDisplayLevel } from '@/voice/volume-meter';
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
  const { micState, settledText, tailText, pendingSend, pause, resume, cancel, sendNow, getLevel } =
    useVoice();
  const orbRef = useRef<HTMLSpanElement | null>(null);

  // Drive the volume orb (09 §3): while listening, sample the mic RMS each frame,
  // smooth it (fast attack / slow release) and write it to the orb's `--mic-level`
  // CSS var, which drives the orb's scale + opacity. This runs off React state so
  // it never re-renders the dock per frame; any non-listening state parks the orb
  // at 0 so it shrinks and fades away.
  useEffect(() => {
    const orb = orbRef.current;
    if (orb === null) {
      return;
    }
    if (micState !== 'listening') {
      orb.style.setProperty('--mic-level', '0');
      return;
    }
    const smoother = new VolumeSmoother();
    let handle = requestAnimationFrame(function tick() {
      const level = smoother.push(toDisplayLevel(getLevel()));
      orb.style.setProperty('--mic-level', level.toFixed(3));
      handle = requestAnimationFrame(tick);
    });
    return () => {
      cancelAnimationFrame(handle);
    };
  }, [micState, getLevel]);

  // One mic tap: pause while listening, otherwise resume/retry (09 §3, §5).
  // Pausing also fires any armed end-of-turn send on the way out (09 §4) — the
  // reducer's `pause` handles that, so stopping the mic = send now + stop.
  const onMicTap = (): void => {
    if (micState === 'listening') {
      pause();
    } else {
      resume();
    }
  };

  const hasTranscript = settledText !== '' || tailText !== '';
  // The X discards the un-committed (still-forming) utterance (09 §4); show it
  // whenever there is an un-committed tail to discard.
  const showCancel = tailText !== '';
  // The send button fires the armed end-of-turn final immediately, skipping the
  // post-turn-end grace window (09 §4); show it whenever a send is armed.
  const showSend = pendingSend;

  return (
    <div data-role="dock" data-dock-state={micState}>
      {hasTranscript && (
        <div data-role="dock-transcript" data-dock-state={micState}>
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
            <svg viewBox="0 0 24 24" width="18" height="18" aria-hidden="true">
              <path d="M2 21l21-9L2 3v7l15 2-15 2v7z" fill="currentColor" />
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
            <span data-role="dock-mic-orb" ref={orbRef} />
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
