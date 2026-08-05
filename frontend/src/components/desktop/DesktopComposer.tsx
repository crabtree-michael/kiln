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
// send with Enter. That handover is the whole design — it is what makes one field
// honest rather than two stacked ones wearing the same border.
//
// **There is literally one buffer, and it is the store's.** The field has no local
// draft state: focusing it hands the transcript over for editing (`beginEdit`) and
// every keystroke writes straight back through `editTranscript`, so what you are
// looking at is always the utterance that would send. That is what lets the
// post-turn-end auto-send *pause* rather than die when you correct something
// (09 §4a): the send stays armed on the text you are editing, its countdown frozen
// for as long as the field has focus, and picks up where it left off when you
// click away. A local draft would have had to cancel the send to avoid firing the
// stale words — which is what this used to do.
//
// It is deliberately NOT the mobile dock's `keyboardMode`. That toggle is modal —
// entering it stops the mic, leaving it restarts it — because a phone has room
// for one input at a time. Here there is no mode to be in: one field, two ways to
// put words in it. Both still POST through the identical seam (`sendNow` →
// `POST /api/message`, 07 §4 / 09), so the brain cannot tell them apart.
//
// It is not a form and it never becomes one (13 §7): no title field, no priority
// select, no ticket-creation dialog.
import { useEffect, useRef, type JSX, type KeyboardEvent } from 'react';
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
 * shows is exactly what the store would send. */
function spokenText(settled: string, tail: string): string {
  return [settled, tail]
    .filter((part) => part !== '')
    .join(' ')
    .trim();
}

export function DesktopComposer(): JSX.Element {
  const {
    micState,
    settledText,
    tailText,
    editing,
    beginEdit,
    editTranscript,
    endEdit,
    cancel,
    sendNow,
  } = useVoice();
  const inputRef = useRef<HTMLTextAreaElement>(null);
  // Set when focus arrives from a keyboard jump ("/" or Cmd-K) rather than a click:
  // a click carries its own caret position (you clicked the word you meant to fix),
  // a jump does not, so the caret belongs after the words.
  const caretToEndRef = useRef(false);

  const spoken = spokenText(settledText, tailText);
  // While words are arriving the field renders them as two-tone text — settled ink,
  // ghosted tail, blinking caret — which a textarea cannot do; the textarea is
  // still there, over the words and transparent, so a click or "/" lands in it and
  // hands over. The moment it does, `editing` turns that off and the same words are
  // simply in the field, editable. One field, one buffer, two readings of it.
  const hearing = spoken !== '' && !editing;
  const canSend = spoken !== '';

  const send = (): void => {
    // Everything in the field is the store's transcript now — typed, spoken or
    // corrected — so there is one send path: commit the displayed text and release
    // the mic (09 §4, §3a). On failure the store keeps the words on screen (no
    // modal, no lost sentence); on success it clears back to idle.
    if (!canSend) {
      return;
    }
    sendNow();
  };

  const onInputKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>): void => {
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault();
      send();
      return;
    }
    if (event.key === 'Escape') {
      // "…and getting back out of it" (13 §9). Escape returns focus to the page
      // so the keyboard pass can continue in the feed; the text is kept, since
      // losing a half-typed thought to a stray Escape is not calm. The blur ends
      // the edit, so a paused auto-send starts counting again from here.
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
      caretToEndRef.current = true;
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
  }, [settledText, hearing]);

  // A keyboard jump into a field that already holds words drops the caret wherever
  // it last was — usually the very start, in front of the sentence. Put it after
  // the words so typing continues them. Runs once the handover has folded any tail
  // in (`editing` flips with it), so it measures the final text.
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
  }, [settledText, editing]);

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
            value={settledText}
            onChange={(event) => {
              editTranscript(event.target.value);
            }}
            // Putting a cursor in the field IS the handover: the mic stops, the
            // still-forming tail folds into the text, and the armed auto-send
            // freezes (09 §4a). Focus rather than the first keystroke, so the words
            // are already editable — and already not about to fly off — by the time
            // the first character arrives.
            onFocus={beginEdit}
            // Clicking away hands it back: the frozen countdown resumes from where
            // it stopped, now pointed at the corrected sentence.
            onBlur={endEdit}
            onKeyDown={onInputKeyDown}
            // The prompt goes the instant there are words to read, not when the
            // first utterance settles. While speech is on screen the textarea is
            // transparent OVER the heard block, and its value is only the settled
            // ink — so through the whole interim stretch (tail text, nothing
            // settled yet) an empty textarea would paint "Talk, or type…" straight
            // across the words being spoken. `hearing` is exactly "there is
            // transcript on screen", interim included, so it is what gates this.
            placeholder={hearing ? undefined : 'Talk, or type…'}
            aria-label="Say something"
          />
        </div>
        <div data-role="desktop-composer-actions">
          {/* One pair of controls for both ways of putting words here: send
              commits whatever the field holds, clear empties it. There is no
              separate voice send and typed send, because there is no separate
              voice field and typed field. */}
          {canSend && (
            <button type="button" data-role="dock-cancel" aria-label="Clear" onClick={cancel}>
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
