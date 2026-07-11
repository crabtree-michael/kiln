// The mic-input button with its volume-reactive orb (09 §3), shared verbatim by
// the dock and the proposal detail sheet so both surfaces drive the *same* mic —
// same glyph, same listening glow, same tap-to-toggle — off the one voice store.
// Presentational consumer of `useVoice()`: it renders `{ micState, connecting }`,
// samples `getLevel()` each frame to colour the glow, and toggles `pause`/`resume`
// on tap. It does NOT send (that is the dock's send button). Requires a
// `VoiceProvider` ancestor, so it is only mounted on the primary screen — the
// /debug board opens the detail sheet without one and never passes it in.
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
}

export function MicButton({ showLabel = false }: MicButtonProps): JSX.Element {
  const { micState, connecting, pause, resume, getLevel } = useVoice();
  const orbRef = useRef<HTMLSpanElement | null>(null);

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
    if (micState !== 'listening') {
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
  }, [micState, getLevel]);

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
