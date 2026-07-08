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
import { useEffect, useRef, useState, type JSX, type KeyboardEvent } from 'react';
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

export function Dock(): JSX.Element {
  const {
    micState,
    connecting,
    settledText,
    tailText,
    pause,
    resume,
    cancel,
    sendNow,
    getLevel,
    keyboardMode,
    openKeyboard,
    closeKeyboard,
    submitText,
  } = useVoice();
  const transcriptRef = useRef<HTMLDivElement | null>(null);
  const orbRef = useRef<HTMLSpanElement | null>(null);
  const inputRef = useRef<HTMLTextAreaElement | null>(null);
  // The typed draft is pure view state local to the dock — the store only sees it
  // on submit (via `submitText`). Kept out of the voice machine so keystrokes
  // don't churn the transcript reducer.
  const [draft, setDraft] = useState('');
  // Whether the soft keyboard is up, tracked via the field's focus. Drives the
  // dismiss button, which only makes sense while the keyboard is actually
  // showing — the field can stay mounted in keyboard mode with focus dropped
  // (the user tapped the dismiss button), so this is finer-grained than
  // `keyboardMode`.
  const [keyboardVisible, setKeyboardVisible] = useState(false);

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
  // The overlay field is shown for the live voice transcript OR the keyboard input
  // (they reuse the same container). The keyboard toggle only appears in the
  // resting voice state — never mid-dictation (where the flanks are send + X) and
  // never in keyboard mode — so it never competes with the X for the right slot.
  const showField = hasTranscript || keyboardMode;
  const showKeyboardToggle = !keyboardMode && !hasTranscript;

  // Submit the typed draft through the same seam as a spoken utterance. Clear the
  // field only on a successful POST; on failure keep the text so the user can
  // retry (mirrors the commit effect's no-modal stance, 09 §4). Stay in keyboard
  // mode after a send so consecutive messages can be typed.
  const submitDraft = (): void => {
    const text = draft.trim();
    if (text === '') {
      return;
    }
    void submitText(text).then((sent) => {
      if (sent) {
        setDraft('');
      }
    });
  };

  const onInputKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>): void => {
    // Enter sends; Shift+Enter inserts a newline for a multi-line message.
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault();
      submitDraft();
    }
  };

  // Wipe the typed draft in one tap — the standard trailing "×" affordance for a
  // text field. Only the text is discarded (unlike the voice-mode X, which also
  // tears down the transcript): stay in keyboard mode and refocus the field so
  // the soft keyboard stays up and the user can retype straight away.
  const clearDraft = (): void => {
    setDraft('');
    inputRef.current?.focus();
  };

  // Hand input back to voice: discard the un-sent draft (like the X on a voice
  // transcript) so reopening starts clean, leave keyboard mode, and turn the mic
  // on via the same `resume` flow the mic button uses.
  const switchToVoice = (): void => {
    setDraft('');
    closeKeyboard();
    resume();
  };

  // Drop the field's focus to close the soft keyboard, staying in keyboard mode
  // with the draft intact. The pointer-down handler on the button keeps the
  // field focused through the tap (so the button doesn't vanish before its
  // click lands); this blur is the one deliberate dismissal.
  const dismissKeyboard = (): void => {
    inputRef.current?.blur();
  };

  // Focus the field the moment keyboard mode opens so the user can type straight
  // away, and grow it with its content (bounded by the container's own cap).
  useEffect(() => {
    if (!keyboardMode) {
      return;
    }
    const el = inputRef.current;
    if (el === null) {
      return;
    }
    el.focus();
  }, [keyboardMode]);

  useEffect(() => {
    const el = inputRef.current;
    if (el === null) {
      return;
    }
    el.style.height = 'auto';
    el.style.height = `${el.scrollHeight.toString()}px`;
  }, [draft, keyboardMode]);

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
  }, [showField]);

  // Keep the latest words in view as text streams in. The transcript grows
  // upward but scrolls internally (`overflow-y: auto`, `max-height: 28vh`), and
  // text flows top-to-bottom, so the newest words land at the bottom. Once the
  // content exceeds the cap the container stops auto-tracking, so on every
  // settled/tail update we re-pin `scrollTop` to the bottom — a no-op while the
  // transcript is short enough not to overflow.
  useEffect(() => {
    const el = transcriptRef.current;
    if (el === null) {
      return;
    }
    el.scrollTop = el.scrollHeight;
  }, [settledText, tailText]);

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
    <div data-role="dock" data-dock-state={keyboardMode ? 'keyboard' : micState}>
      {showField && (
        <div
          data-role="dock-transcript"
          data-dock-state={keyboardMode ? 'keyboard' : micState}
          ref={transcriptRef}
        >
          {keyboardMode ? (
            // Reuse the very container that shows the live voice transcript, swapping
            // its read-only spans for an editable field (09 §4 seam, keyboard input).
            <>
              <textarea
                data-role="dock-input"
                ref={inputRef}
                rows={1}
                value={draft}
                onChange={(event) => {
                  setDraft(event.target.value);
                }}
                onKeyDown={onInputKeyDown}
                onFocus={() => {
                  setKeyboardVisible(true);
                }}
                onBlur={() => {
                  setKeyboardVisible(false);
                }}
                placeholder="Type a message…"
                aria-label="Message"
              />
              {draft !== '' && (
                // The clear (×) affordance, pinned to the field's top-right corner
                // while there is text to wipe. Preventing the default on
                // pointer-down keeps the field focused through the tap (as the
                // dismiss button does) so clearing never drops the soft keyboard.
                <button
                  type="button"
                  data-role="dock-clear"
                  aria-label="Clear text"
                  onMouseDown={(event) => {
                    event.preventDefault();
                  }}
                  onClick={clearDraft}
                >
                  <span aria-hidden="true">×</span>
                </button>
              )}
            </>
          ) : (
            <>
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
            </>
          )}
        </div>
      )}

      <div data-role="dock-controls" data-mode={keyboardMode ? 'keyboard' : 'voice'}>
        {keyboardMode ? (
          <>
            {/* Leave keyboard mode → turn the mic back on for voice input. */}
            <button type="button" data-role="dock-voice" aria-label="Talk" onClick={switchToVoice}>
              <svg
                viewBox="0 0 24 24"
                width="22"
                height="22"
                aria-hidden="true"
                fill="none"
                stroke="currentColor"
                strokeWidth="1.5"
                strokeLinecap="round"
                strokeLinejoin="round"
              >
                <rect x="9" y="3" width="6" height="11" rx="3" />
                <path d="M5 11a7 7 0 0 0 14 0" />
                <path d="M12 18v3" />
              </svg>
            </button>
            {keyboardVisible && (
              // Same keyboard glyph as the open button, but with a down chevron —
              // tapping it drops focus to hide the soft keyboard. Preventing the
              // default on pointer-down keeps the field focused through the tap so
              // the button doesn't unmount out from under its own click.
              <button
                type="button"
                data-role="dock-dismiss"
                aria-label="Dismiss keyboard"
                onMouseDown={(event) => {
                  event.preventDefault();
                }}
                onClick={dismissKeyboard}
              >
                <svg
                  viewBox="0 0 24 24"
                  width="22"
                  height="22"
                  aria-hidden="true"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="1.5"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                >
                  <rect x="2.5" y="4.5" width="19" height="11" rx="2.5" />
                  <path d="M6 8h.01M9.8 8h.01M13.6 8h.01M17.4 8h.01" />
                  <path d="M6 11.3h.01M9.8 11.3h.01M13.6 11.3h.01M17.4 11.3h.01" />
                  <path d="M8.5 13.6h7" />
                  <path d="M8.8 18l3.2 3 3.2-3" />
                </svg>
              </button>
            )}
            <button
              type="button"
              data-role="dock-send"
              aria-label="Send"
              onClick={submitDraft}
              disabled={draft.trim() === ''}
            >
              <svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true">
                <path d="M12 4l-8 8h5v8h6v-8h5z" fill="currentColor" />
              </svg>
            </button>
          </>
        ) : (
          <>
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
              // The setup window (tapped on, socket not yet recording): the mic is
              // already `listening` but not capturing, so flag it so the CSS swaps
              // the live glow for a spinner ring — the user waits to speak instead
              // of getting cut off (09 §3).
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
              <span data-role="dock-label">{connecting ? 'Connecting…' : LABELS[micState]}</span>
            </button>

            {showCancel && (
              <button type="button" data-role="dock-cancel" aria-label="Cancel" onClick={cancel}>
                <span aria-hidden="true">×</span>
              </button>
            )}

            {showKeyboardToggle && (
              <button
                type="button"
                data-role="dock-keyboard"
                aria-label="Type a message"
                onClick={openKeyboard}
              >
                <svg
                  viewBox="0 0 24 24"
                  width="22"
                  height="22"
                  aria-hidden="true"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="1.5"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                >
                  <rect x="2.5" y="4.5" width="19" height="11" rx="2.5" />
                  <path d="M6 8h.01M9.8 8h.01M13.6 8h.01M17.4 8h.01" />
                  <path d="M6 11.3h.01M9.8 11.3h.01M13.6 11.3h.01M17.4 11.3h.01" />
                  <path d="M8.5 13.6h7" />
                  <path d="M8.8 21l3.2-3 3.2 3" />
                </svg>
              </button>
            )}
          </>
        )}
      </div>
    </div>
  );
}
