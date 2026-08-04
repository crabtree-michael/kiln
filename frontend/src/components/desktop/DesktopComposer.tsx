// The desktop's way of saying something (13 §7, D5) — one quiet line under the
// feed.
//
// The modality flips here and nowhere else: **typing is primary, voice is
// secondary.** At a desk the keyboard is already under your hands, typing is
// silent in a room with other people in it, and a typed sentence is more precise
// than a spoken one when it contains a filename. So the field is always there
// and always focusable, and the mic is an affordance *on* the line rather than
// the centrepiece the mobile dock makes it.
//
// It is deliberately NOT the mobile dock's `keyboardMode`. That toggle is
// modal — entering it stops the mic, leaving it restarts it — because a phone
// has room for one input at a time. A desk does not have that constraint: the
// field and the mic coexist, and the user picks per-utterance without flipping a
// mode. Both still POST through the identical seam (`submitText` →
// `POST /api/message`, 07 §4 / 09), so the brain cannot tell them apart.
//
// It is not a form and it never becomes one (13 §7): no title field, no priority
// select, no ticket-creation dialog.
import { useEffect, useRef, useState, type JSX, type KeyboardEvent } from 'react';
import { useVoice } from '@/voice/voice-context';
import { MicButton } from '@/components/MicButton';

/** Does this element already absorb typing? Used by the global focus binding
 * below so "/" types a slash when the user is mid-sentence somewhere else
 * instead of yanking focus out from under them. */
function isTextEntry(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) {
    return false;
  }
  return (
    target instanceof HTMLInputElement ||
    target instanceof HTMLTextAreaElement ||
    target.isContentEditable
  );
}

export function DesktopComposer(): JSX.Element {
  const { micState, settledText, tailText, cancel, sendNow, submitText } = useVoice();
  const inputRef = useRef<HTMLTextAreaElement>(null);
  // The typed draft is view state local to this component — the store only sees
  // it on submit. Same stance as the dock's draft: keystrokes must not churn the
  // voice reducer.
  const [draft, setDraft] = useState('');

  const hasTranscript = settledText !== '' || tailText !== '';
  const canSend = draft.trim() !== '';

  const submitDraft = (): void => {
    const text = draft.trim();
    if (text === '') {
      return;
    }
    // Clear only on a successful POST; on failure keep the text so the user can
    // retry (mirrors the dock — no modal, no lost sentence).
    void submitText(text).then((sent) => {
      if (sent) {
        setDraft('');
      }
    });
  };

  const onInputKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>): void => {
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault();
      submitDraft();
      return;
    }
    if (event.key === 'Escape') {
      // "…and getting back out of it" (13 §9). Escape returns focus to the page
      // so the keyboard pass can continue in the feed; the draft is kept, since
      // losing a half-typed thought to a stray Escape is not calm.
      event.currentTarget.blur();
    }
  };

  // "Jumping to the input from anywhere" (13 §9). `/` is the binding when focus
  // is not already in a text entry, with Cmd/Ctrl-K as the modifier form for
  // anyone whose muscle memory has it. 13 §12 leaves concrete bindings open —
  // this is a first pass, not a claim to have settled them.
  useEffect(() => {
    const onKeyDown = (event: globalThis.KeyboardEvent): void => {
      const isSlash = event.key === '/' && !event.metaKey && !event.ctrlKey && !event.altKey;
      const isCommandK = event.key.toLowerCase() === 'k' && (event.metaKey || event.ctrlKey);
      if (!isSlash && !isCommandK) {
        return;
      }
      if (isSlash && isTextEntry(event.target)) {
        return;
      }
      const input = inputRef.current;
      if (input === null) {
        return;
      }
      event.preventDefault();
      input.focus();
    };
    document.addEventListener('keydown', onKeyDown);
    return () => {
      document.removeEventListener('keydown', onKeyDown);
    };
  }, []);

  // Grow the field with its content, bounded by the container's own cap (CSS).
  useEffect(() => {
    const el = inputRef.current;
    if (el === null) {
      return;
    }
    el.style.height = 'auto';
    el.style.height = `${el.scrollHeight.toString()}px`;
  }, [draft]);

  return (
    <div data-role="desktop-composer" data-mic-state={micState}>
      {hasTranscript && (
        // The live transcript sits ABOVE the line, in flow — not as an
        // upward-growing overlay. The mobile dock floats it because a phone has
        // no spare height; at a desk the composer can simply be taller for a
        // moment, and an in-flow block can't paint over the feed it is beneath.
        <div data-role="desktop-transcript">
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
      <div data-role="desktop-composer-row">
        <textarea
          data-role="desktop-input"
          ref={inputRef}
          rows={1}
          value={draft}
          onChange={(event) => {
            setDraft(event.target.value);
          }}
          onKeyDown={onInputKeyDown}
          placeholder="Say something…"
          aria-label="Say something"
        />
        <div data-role="desktop-composer-actions">
          {hasTranscript ? (
            // Voice is mid-utterance: send and clear flank the mic, exactly as
            // they do in the dock (same handlers, same `dock-send`/`dock-cancel`
            // skin), so the orb stays tappable to stop listening rather than
            // being swapped away by `MicButton`'s `sendable` mode.
            <>
              <button type="button" data-role="dock-cancel" aria-label="Clear" onClick={cancel}>
                <span aria-hidden="true">×</span>
              </button>
              <MicButton />
              <button type="button" data-role="dock-send" aria-label="Send" onClick={sendNow}>
                <svg viewBox="0 0 24 24" width="20" height="20" aria-hidden="true">
                  <path d="M12 4l-8 8h5v8h6v-8h5z" fill="currentColor" />
                </svg>
              </button>
            </>
          ) : (
            <>
              <MicButton />
              <button
                type="button"
                data-role="desktop-send"
                aria-label="Send"
                onClick={submitDraft}
                disabled={!canSend}
              >
                <svg viewBox="0 0 24 24" width="20" height="20" aria-hidden="true">
                  <path d="M12 4l-8 8h5v8h6v-8h5z" fill="currentColor" />
                </svg>
              </button>
            </>
          )}
        </div>
      </div>
    </div>
  );
}
