// The dock (08 §2 dock region, 09 §3–§4): the mic button + live transcript + the
// cancel (X). Presentational consumer of the voice store — all lifecycle and
// commit logic lives in `voice-store`/`commit-machine`; this file only renders
// `useVoice()`'s `{ micState, settledText, tailText }` and forwards taps to
// `pause`/`resume`/`cancel`.
//
// The 08 §F selector surface is preserved verbatim (`data-role="dock"`,
// `"dock-talk"`, `aria-label="Talk"`, and the mic-glyph sub-elements) so
// `PrimaryScreen.css` and existing selectors keep working; `data-dock-state` now
// reflects the live `micState` instead of the placeholder `"idle"`.
import type { JSX } from 'react';
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
  const { micState, settledText, tailText, pause, resume, cancel } = useVoice();

  // One mic tap: pause while listening, otherwise resume/retry (09 §3, §5).
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
        <button
          type="button"
          data-role="dock-talk"
          data-dock-state={micState}
          aria-label="Talk"
          aria-pressed={micState === 'listening'}
          onClick={onMicTap}
        >
          <span data-role="dock-mic" aria-hidden="true">
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
