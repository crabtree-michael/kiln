// The desktop's way of saying something (13 §7, D5a) — a big mic and one field,
// side by side under the feed.
//
// **Voice is primary again, and typing is the same field.** The mic is the large
// object on the left, at the dock's full scale, because Kiln is an ambient
// voice-first thing at a desk too — the keyboard being under your hands is a
// reason to keep typing *available*, not a reason to make it the shape of the
// surface (D5a supersedes D5).
//
// What the desk earns is that talking and typing land in the SAME PLACE. There is
// one text surface to the mic's right, and it is both the live transcript and the
// draft you type: speech fills it as you talk, and the moment you put a cursor in
// it the words you just said become editable text you can fix a filename in and
// send with Enter. That handover is the whole design (`adoptTranscript` below) —
// it is what makes one field honest rather than two stacked ones wearing the same
// border.
//
// It is deliberately NOT the mobile dock's `keyboardMode`. That toggle is modal —
// entering it stops the mic, leaving it restarts it — because a phone has room
// for one input at a time. Here there is no mode to be in: one field, two ways to
// put words in it. Both still POST through the identical seam (`sendNow` /
// `submitText` → `POST /api/message`, 07 §4 / 09), so the brain cannot tell them
// apart.
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

/** The transcript as one line of text — settled ink plus whatever tail is still
 * forming. Same join the commit machine's own `sendNow` uses, so what the field
 * hands to the draft is exactly what the store would have sent. */
function spokenText(settled: string, tail: string): string {
  return [settled, tail]
    .filter((part) => part !== '')
    .join(' ')
    .trim();
}

export function DesktopComposer(): JSX.Element {
  const { micState, settledText, tailText, pause, cancel, sendNow, submitText } = useVoice();
  const inputRef = useRef<HTMLTextAreaElement>(null);
  // Set when the draft was just replaced wholesale (a transcript adopted into
  // it): the caret then belongs at the end of the words, not wherever it was.
  const caretToEndRef = useRef(false);
  // The typed draft is view state local to this component — the store only sees
  // it on submit. Same stance as the dock's draft: keystrokes must not churn the
  // voice reducer.
  const [draft, setDraft] = useState('');

  const spoken = spokenText(settledText, tailText);
  const typed = draft.trim();
  // While there is speech on screen the field renders it; the textarea is still
  // there, over the words and transparent, so a click or "/" lands in it and
  // hands over (below). One field, two sources.
  const hearing = spoken !== '';
  const canSend = typed !== '' || spoken !== '';

  /** The handover: the user has put a cursor in the field while words were being
   * heard, so the utterance stops being the store's and becomes the draft's.
   *
   * `pause` first — taking the keyboard means you have taken over, so the mic
   * stops and, more importantly, the armed end-of-turn send is disarmed (09 §4);
   * a sentence must not fly off mid-edit. `cancel` then clears the store's copy
   * so the same words cannot be both in the field and in a pending commit. What
   * was heard is appended to anything already typed, so neither half is lost. */
  const adoptTranscript = (): void => {
    if (spoken === '') {
      return;
    }
    pause();
    cancel();
    caretToEndRef.current = true;
    setDraft((current) => (current === '' ? spoken : `${current} ${spoken}`));
  };

  const send = (): void => {
    if (typed === '' && spoken === '') {
      return;
    }
    if (typed === '') {
      // Pure speech: the store's own seam, unchanged — it commits the displayed
      // transcript and releases the mic (09 §4, §3a).
      sendNow();
      return;
    }
    // Anything typed goes out as one message, with any heard words appended, so
    // a half-typed thought plus a spoken finish is a single utterance rather than
    // two. Take the transcript off the store first (as the handover does) so it
    // cannot also auto-send behind us.
    const text = spoken === '' ? typed : `${typed} ${spoken}`;
    if (spoken !== '') {
      pause();
      cancel();
      // The heard half now lives only here, so it has to be in the draft to
      // survive a failed POST below — the store no longer has a copy.
      setDraft(text);
    }
    // Clear only on a successful POST; on failure keep the text so the user can
    // retry (mirrors the dock: no modal, no lost sentence).
    void submitText(text).then((sent) => {
      if (sent) {
        setDraft('');
      }
    });
  };

  const clear = (): void => {
    cancel();
    setDraft('');
  };

  const onInputKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>): void => {
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault();
      send();
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
  // While the field is showing speech the textarea is absolutely positioned over
  // those words (CSS) and the heard block sizes the box instead — so drop the
  // inline height, which would otherwise beat the `height: 100%` that stretches
  // it over the full block.
  useEffect(() => {
    const el = inputRef.current;
    if (el === null) {
      return;
    }
    if (hearing) {
      el.style.height = '';
      return;
    }
    el.style.height = 'auto';
    el.style.height = `${el.scrollHeight.toString()}px`;
  }, [draft, hearing]);

  // After a transcript is adopted the words are suddenly there in front of the
  // caret; put it after them so typing continues the sentence rather than
  // landing in the middle of it.
  useEffect(() => {
    if (!caretToEndRef.current) {
      return;
    }
    caretToEndRef.current = false;
    const el = inputRef.current;
    if (el === null) {
      return;
    }
    el.setSelectionRange(el.value.length, el.value.length);
  }, [draft]);

  return (
    <div data-role="desktop-composer" data-mic-state={micState}>
      {/* The mic at full dock scale, and first in the row: at a desk as on a
          phone, talking is the way you say things to Kiln (13 D5a). Nothing else
          in this component is allowed to out-weigh it. */}
      <MicButton />
      <div data-role="desktop-field" data-hearing={hearing ? 'true' : undefined}>
        <div data-role="desktop-field-text">
          {hearing && (
            // The words as they are heard, in the field itself — not a second
            // block stacked above it. They sit in normal flow so they set the
            // field's height; the textarea over them is transparent, so this is
            // what you read while the same element is what you click into.
            <div data-role="desktop-heard">
              {draft !== '' && <span data-role="dock-settled">{draft} </span>}
              {settledText !== '' && <span data-role="dock-settled">{settledText}</span>}
              {tailText !== '' && (
                <span data-role="dock-tail" data-ghost="true">
                  {settledText === '' ? tailText : ` ${tailText}`}
                </span>
              )}
              {micState === 'listening' && (
                <span data-role="dock-caret" aria-hidden="true">
                  |
                </span>
              )}
            </div>
          )}
          <textarea
            data-role="desktop-input"
            ref={inputRef}
            rows={1}
            value={draft}
            onChange={(event) => {
              setDraft(event.target.value);
            }}
            // Putting a cursor in the field IS the handover — see
            // `adoptTranscript`. Focus rather than the first keystroke, so the
            // words are already editable (and the auto-send already disarmed) by
            // the time the first character arrives.
            onFocus={adoptTranscript}
            onKeyDown={onInputKeyDown}
            placeholder="Talk, or type…"
            aria-label="Say something"
          />
        </div>
        <div data-role="desktop-composer-actions">
          {/* One pair of controls for both ways of putting words here: send
              commits whatever the field holds, clear empties it. There is no
              separate voice send and typed send, because there is no separate
              voice field and typed field. */}
          {canSend && (
            <button type="button" data-role="dock-cancel" aria-label="Clear" onClick={clear}>
              <span aria-hidden="true">×</span>
            </button>
          )}
          <button
            type="button"
            data-role="desktop-send"
            aria-label="Send"
            onClick={send}
            disabled={!canSend}
          >
            <svg viewBox="0 0 24 24" width="20" height="20" aria-hidden="true">
              <path d="M12 4l-8 8h5v8h6v-8h5z" fill="currentColor" />
            </svg>
          </button>
        </div>
      </div>
    </div>
  );
}
