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
import { MicButton } from '@/components/MicButton';
import { SystemAlertBand } from '@/components/SystemAlertBand';
import type { SystemAlert } from '@/transport/transport';

export interface DockProps {
  /** Persistent system-health alerts, surfaced as the permanent error band at the
   * very top of the dock (above the controls). Rendered HERE rather than as a
   * dock-region sibling so the live-transcript overlay — anchored to the dock's
   * top edge (`bottom: 100%`) — grows ABOVE the band instead of painting over it
   * (the transcript is opaque and out-ranks an in-flow sibling). Defaults to none
   * so presentational tests can mount the dock without board state. */
  alerts?: SystemAlert[];
}

export function Dock({ alerts = [] }: DockProps): JSX.Element {
  const {
    micState,
    settledText,
    tailText,
    resume,
    cancel,
    sendNow,
    sendImminent,
    delaySend,
    keyboardMode,
    openKeyboard,
    closeKeyboard,
    submitText,
  } = useVoice();
  const transcriptRef = useRef<HTMLDivElement | null>(null);
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

  // Both side controls appear whenever there is any transcript text on screen —
  // interim or settled, listening or paused (the "stuck" case) — so the user can
  // always send the shown transcript (send button) or clear it (X). Neither is
  // gated on listening state or on a final having landed (09 §4).
  const hasTranscript = settledText !== '' || tailText !== '';
  const showSend = hasTranscript;
  const showCancel = hasTranscript;
  // The "+10" delay control appears only in the final stretch before an armed
  // end-of-turn auto-send fires (09 §4, `sendImminent`) — not for the whole
  // countdown, and never in the "stuck"/paused case where there is transcript but
  // nothing is about to fire. It floats above the mic as the deadline nears, giving
  // the user a way to push the send out when they aren't ready; tapping it extends
  // the deadline past the stretch, so the control withdraws until it nears again.
  const showDelay = sendImminent;
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

  return (
    <div data-role="dock" data-dock-state={keyboardMode ? 'keyboard' : micState}>
      {/* The permanent error band sits at the TOP of the dock, in flow above the
          controls. It must be a child of the dock (not a dock-region sibling) so
          the transcript's `bottom: 100%` anchor lands at the band's top and the
          overlay grows above it — see SystemAlertBand / PrimaryScreen.css. Renders
          nothing when there are no alerts, leaving the idle dock untouched. */}
      <SystemAlertBand alerts={alerts} />
      {showField && (
        <div
          data-role="dock-transcript"
          data-dock-state={keyboardMode ? 'keyboard' : micState}
          // When the auto-send bubble is imminent it floats up into the bottom of
          // this overlay; the flag reserves matching bottom padding (CSS) so the
          // transcript text lifts clear rather than the bubble landing on top of it.
          data-send-imminent={showDelay ? 'true' : undefined}
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

            {/* The mic and its "+10" bubble are ONE dock component. The bubble is a
                child of this group (not a free-floating child of the controls row),
                so it is anchored to the mic itself: the two share a grid cell and
                expand/contract together, staying aligned when a toast or the growing
                transcript overlay changes the mic's surroundings — no dock shift. */}
            <div data-role="dock-mic-group">
              {showDelay && (
                // "+10": push the armed auto-send 10s further out. Floats as a bubble
                // centred above the mic (CSS), surfacing only in the final stretch of
                // the countdown; tapping it defers the send and withdraws the bubble.
                <button
                  type="button"
                  data-role="dock-delay"
                  aria-label="Delay auto-send 10 seconds"
                  onClick={delaySend}
                >
                  <span aria-hidden="true">+10</span>
                </button>
              )}

              <MicButton showLabel />
            </div>

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
