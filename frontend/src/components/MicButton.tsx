// The mic-input button with its volume-reactive orb (09 §3), shared verbatim by
// the dock and the proposal detail sheet so both surfaces drive the *same* mic —
// same glyph, same listening glow, same tap-to-toggle — off the one voice store.
// Presentational consumer of `useVoice()`: it renders `{ micState, connecting }`,
// samples `getLevel()` each frame to colour the glow, and toggles `pause`/`resume`
// on tap. Requires a `VoiceProvider` ancestor, so it is only mounted on the
// primary screen — the /debug board opens the detail sheet without one and never
// passes it in.
//
// By default it is mic-only (the dock renders its own send/cancel around it). The
// opt-in `sendable` prop makes it send-aware for compact placements (the ticket
// detail sheet, 08 §5): the moment a transcript is on screen the orb GIVES WAY to
// a send button + a small clear (×) — the same `sendNow`/`cancel` seam and
// `dock-send`/`dock-cancel` skin the dock uses — so the utterance can be committed
// or reset in place without the dock's full controls row.
//
// The 08 §F / 09 §3 selector surface (`data-role="dock-talk"`, `"dock-mic"`, the
// mic-glyph sub-elements, `aria-label="Talk"`) is preserved so `PrimaryScreen.css`
// styles it identically wherever it is placed — no per-surface mic CSS.
import { useEffect, useRef, type JSX } from 'react';
import { useVoice } from '@/voice/voice-context';
import type { MicState } from '@/voice/commit-machine';
import { VolumeSmoother, toDisplayLevel, toGlowResponse } from '@/voice/volume-meter';

// The mic-button copy per state (09 §3 table). Listening is the amber resting
// state; the rest are grey and tap-to-act.
const LABELS: Record<MicState, string> = {
  listening: 'Listening…',
  paused: 'Tap to talk',
  denied: 'Tap to enable mic',
  retry: 'Tap to retry',
};

export interface MicButtonProps {
  /** Whether to show the state-copy label beneath the orb (09 §3). The dock shows
   *  it (`Listening…` / `Tap to talk` / …); compact placements like the proposal
   *  sheet's footer render just the orb and omit it. Defaults off. */
  showLabel?: boolean;
  /** Make the button send-aware: while a transcript is on screen (recording or
   *  paused-with-text) the mic orb is REPLACED by a send button + a small clear
   *  (×), so a compact placement (the ticket detail sheet) can commit or reset the
   *  utterance without the dock's full controls row. Defaults off — the dock passes
   *  nothing and keeps rendering just the orb (it owns its own send/cancel). */
  sendable?: boolean;
}

export function MicButton({ showLabel = false, sendable = false }: MicButtonProps): JSX.Element {
  const { micState, connecting, settledText, tailText, pause, resume, cancel, sendNow, getLevel } =
    useVoice();
  const orbRef = useRef<HTMLSpanElement | null>(null);
  // Send-aware mode swaps the orb for send + clear the moment there is any
  // transcript on screen — interim or settled, listening or paused (the "stuck"
  // case) — mirroring the dock's own send/cancel gate (09 §4).
  const sendMode = sendable && (settledText !== '' || tailText !== '');

  // One mic tap: pause while listening, otherwise resume/retry (09 §3, §5). This
  // only stops/starts the mic — it does NOT send (the dock's send button does).
  // Any transcript stays put while paused so it can still be sent or cleared.
  const onMicTap = (): void => {
    if (micState === 'listening') {
      pause();
    } else {
      resume();
    }
  };

  // Drive the listening glow's colour from live mic loudness (09 §3). While
  // listening, sample the smoothed input level each frame and publish it as
  // `--mic-level` on the orb; `PrimaryScreen.css` maps that white→red. `getLevel`
  // reads the AnalyserNode tap; `VolumeSmoother` gives the fast-attack/slow-release
  // feel; `toGlowResponse` snaps the colour toward red on any real speech. The
  // effect only runs while listening (and cleans the var on stop), so it costs
  // nothing in other states; a no-op where rAF is unavailable (isolated tests).
  useEffect(() => {
    // No orb to drive while send mode has swapped it out (or when not listening),
    // so skip the rAF loop — and, because `sendMode` is a dep, its cleanup fires
    // when the transcript arrives and the orb unmounts, cancelling any live loop.
    if (micState !== 'listening' || sendMode) {
      return;
    }
    const orb = orbRef.current;
    if (orb === null || typeof requestAnimationFrame === 'undefined') {
      return;
    }
    const smoother = new VolumeSmoother();
    let handle = requestAnimationFrame(function tick() {
      const level = smoother.push(toDisplayLevel(getLevel()));
      orb.style.setProperty('--mic-level', toGlowResponse(level).toFixed(3));
      handle = requestAnimationFrame(tick);
    });
    return () => {
      cancelAnimationFrame(handle);
      orb.style.removeProperty('--mic-level');
    };
  }, [micState, getLevel, sendMode]);

  // Send-aware, transcript on screen → the orb gives way to a send button and a
  // small clear (×). Recording continues behind the sheet and the transcript lands
  // in the dock, so there is nothing to render here but the two actions: send
  // commits whatever is shown now (`sendNow`), clear discards it (`cancel`). Reuse
  // the dock's `dock-send`/`dock-cancel` selectors so PrimaryScreen.css skins them
  // identically to the dock's own pair.
  if (sendMode) {
    return (
      <>
        <button type="button" data-role="dock-send" aria-label="Send" onClick={sendNow}>
          <svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true">
            <path d="M12 4l-8 8h5v8h6v-8h5z" fill="currentColor" />
          </svg>
        </button>
        <button type="button" data-role="dock-cancel" aria-label="Clear" onClick={cancel}>
          <span aria-hidden="true">×</span>
        </button>
      </>
    );
  }

  return (
    <button
      type="button"
      data-role="dock-talk"
      data-dock-state={micState}
      // The setup window (tapped on, socket not yet recording): the mic is
      // already `listening` but not capturing, so flag it so the CSS swaps the
      // live glow for a spinner ring — the user waits to speak instead of getting
      // cut off (09 §3).
      data-dock-connecting={connecting ? 'true' : undefined}
      aria-label="Talk"
      aria-pressed={micState === 'listening'}
      onClick={onMicTap}
    >
      <span data-role="dock-mic" aria-hidden="true">
        <span data-role="dock-mic-orb" ref={orbRef} />
        {connecting && <span data-role="dock-mic-spinner" />}
        <span data-role="dock-mic-capsule" />
        <span data-role="dock-mic-arc" />
        <span data-role="dock-mic-stem" />
      </span>
      {showLabel && (
        <span data-role="dock-label">{connecting ? 'Connecting…' : LABELS[micState]}</span>
      )}
    </button>
  );
}
