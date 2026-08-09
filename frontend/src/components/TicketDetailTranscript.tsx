// The message panel in the ticket detail sheet's dock (08 §5, 09 §4) — the live
// voice transcript, and, in typed input, the field that replaces it.
//
// The keyboard half reuses this very container rather than opening one of its
// own, exactly as the main Dock's `dock-transcript` swaps its read-only spans for
// `dock-input`: one panel above the controls, same paper, same centred type, same
// bound — so the sheet has ONE place where the message being composed appears,
// whichever input composed it. The draft it renders is not its own (the Send and
// × for it live in the sheet's other dock slot, so the state is held by
// `TicketDetailHost` — see `useTicketKeyboard`); this file only draws the field
// and grows it with its content.
//
// The voice half:
//
// While the user talks to the brain about the open proposal, the words land here —
// inside the sheet's own dock, above the action buttons — so they can watch the
// transcript without leaving the sheet. (Previously the transcript only landed in
// the main-screen dock *behind* the sheet, hidden under the scrim, so a voice
// session from the proposal sheet gave no on-screen feedback.)
//
// Presentational consumer of `useVoice()`, mirroring the main Dock's transcript
// spans — settled words in ink, the still-forming tail ghosted, a blinking caret
// while listening — but rendered IN FLOW inside the sheet's dock rather than as an
// upward overlay, so the dock grows to fit it (bounded + internally scrolling, see
// TicketDetail.css). It self-gates: nothing renders unless there is transcript text
// on screen, so it is visible only while the user is actively speaking and vanishes
// the moment the utterance is sent or cleared.
//
// It mirrors the Dock's correction affordance too (09 §4a): tapping the words turns
// them into a field over the same text and freezes the armed auto-send while it has
// focus, so a comment on a ticket can be fixed before it goes without racing the
// countdown. Same store surface, same rules — this is a third view of one buffer,
// not a second one.
//
// Requires a `VoiceProvider` ancestor, so — like the sheet's mic (`voiceControl`) —
// only the primary screen wires it in; a read-only sheet opens without one and
// passes nothing, so this never mounts there.
import { useEffect, useRef, type JSX, type KeyboardEvent } from 'react';
import { useVoice } from '@/voice/voice-context';
import type { TicketKeyboard } from '@/components/use-ticket-keyboard';

export interface TicketDetailTranscriptProps {
  /** The sheet's typed-input state, owned by `TicketDetailHost` and shared with
   *  the footer cluster that carries this field's Send and ×. Required rather
   *  than optional so a shell cannot mount the panel with no way to type into
   *  it while the footer offers the toggle. */
  keyboard: TicketKeyboard;
}

export function TicketDetailTranscript({
  keyboard,
}: TicketDetailTranscriptProps): JSX.Element | null {
  const { micState, settledText, tailText, editing, beginEdit, editTranscript, endEdit } =
    useVoice();
  const ref = useRef<HTMLDivElement | null>(null);
  const editRef = useRef<HTMLTextAreaElement | null>(null);
  // Whether the tap that started the edit landed on THIS line. `editing` is one
  // flag on one store and the main Dock is still mounted behind the sheet's scrim,
  // so both surfaces swap in a field when an edit begins — but only the tapped one
  // may take the caret, or the sheet's field loses focus to a dock it is covering.
  const startedEditRef = useRef(false);

  // Keep the newest words in view as text streams in. The line is bounded and
  // scrolls internally (see TicketDetail.css); text flows top-to-bottom, so on
  // every settled/tail update we re-pin scrollTop to the bottom — a no-op while
  // the transcript is short enough not to overflow (mirrors the main Dock).
  useEffect(() => {
    const el = ref.current;
    if (el === null) {
      return;
    }
    el.scrollTop = el.scrollHeight;
  }, [settledText, tailText]);

  // Put the user straight into the field when the tap takes the words over, caret
  // after them (the tap target is the whole utterance, so there is no offset to
  // honour), and grow the field with its content inside the line's own cap.
  useEffect(() => {
    if (!editing) {
      startedEditRef.current = false;
      return;
    }
    if (!startedEditRef.current) {
      return;
    }
    startedEditRef.current = false;
    const el = editRef.current;
    if (el === null) {
      return;
    }
    el.focus();
    el.setSelectionRange(el.value.length, el.value.length);
  }, [editing]);

  // Closing the sheet mid-correction removes a focused field, and removing a
  // focused element fires no blur — so end the edit here or the store sits
  // `editing` forever with the auto-send frozen and nobody in the field. Read
  // through refs so this is a mount-only cleanup that can't re-run and end an edit
  // early. (The Dock has no equivalent: it lives as long as the screen does, and a
  // mic tap clears `editing` anyway.)
  const editingRef = useRef(editing);
  const endEditRef = useRef(endEdit);
  useEffect(() => {
    editingRef.current = editing;
    endEditRef.current = endEdit;
  }, [editing, endEdit]);
  useEffect(() => {
    return () => {
      if (editingRef.current) {
        endEditRef.current();
      }
    };
  }, []);

  useEffect(() => {
    const el = editRef.current;
    if (el === null) {
      return;
    }
    el.style.height = 'auto';
    el.style.height = `${el.scrollHeight.toString()}px`;
  }, [settledText, editing]);

  // Typed input: put the caret in the field the moment the mode opens, so the tap
  // on the keyboard toggle is the only tap it costs to start writing. Unlike the
  // main Dock's field there is no race to arbitrate here — the sheet's typed mode
  // is its own flag, so the dock behind the scrim never opens a second field to
  // lose the caret to (see `useTicketKeyboard`).
  const keyboardOpen = keyboard.open;
  const inputRef = keyboard.inputRef;
  useEffect(() => {
    if (!keyboardOpen) {
      return;
    }
    inputRef.current?.focus();
  }, [keyboardOpen, inputRef]);

  // ...and grow it with its content, exactly as the correction field above does —
  // the panel's own 45dvh cap and internal scroll still bound it.
  useEffect(() => {
    const el = inputRef.current;
    if (el === null) {
      return;
    }
    el.style.height = 'auto';
    el.style.height = `${el.scrollHeight.toString()}px`;
  }, [keyboard.draft, keyboardOpen, inputRef]);

  const onInputKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>): void => {
    // Enter sends; Shift+Enter inserts a newline for a multi-line message. Same
    // bargain the dock's field strikes.
    if (event.key === 'Enter' && !event.shiftKey) {
      event.preventDefault();
      keyboard.submit();
    }
  };

  // Typed input takes the panel over wholesale: `begin` stopped the mic and
  // dropped whatever it had heard, so there is no transcript underneath to show
  // and nothing to stack it above.
  if (keyboardOpen) {
    return (
      <div data-role="ticket-detail-transcript" data-dock-state="keyboard">
        <textarea
          data-role="ticket-detail-input"
          ref={inputRef}
          rows={1}
          value={keyboard.draft}
          onChange={(event) => {
            keyboard.setDraft(event.target.value);
          }}
          onKeyDown={onInputKeyDown}
          placeholder="Type a message…"
          aria-label="Message"
        />
      </div>
    );
  }

  // Visible only when there is a transcript on screen — i.e. only while the user
  // is actively speaking (text clears the moment the utterance is sent or the ×
  // discards it). Same gate the main Dock uses for its voice field, plus the edit
  // in progress, so a correction that empties the text doesn't pull the field out
  // from under its own caret.
  const hasTranscript = settledText !== '' || tailText !== '';
  if (!hasTranscript && !editing) {
    return null;
  }

  return (
    <div
      data-role="ticket-detail-transcript"
      data-dock-state={micState}
      data-editing={editing ? 'true' : undefined}
      ref={ref}
    >
      {editing ? (
        <textarea
          data-role="ticket-detail-transcript-input"
          ref={editRef}
          rows={1}
          value={settledText}
          onChange={(event) => {
            editTranscript(event.target.value);
          }}
          onBlur={endEdit}
          aria-label="Edit transcript"
        />
      ) : (
        <button
          type="button"
          data-role="ticket-detail-transcript-edit"
          // The words ride in the name, not just the action — an `aria-label` of
          // "Edit transcript" alone would swallow the transcript this wraps (same
          // reasoning as the Dock's).
          aria-label={`Edit transcript: ${[settledText, tailText].filter((part) => part !== '').join(' ')}`}
          onClick={() => {
            // Claim the caret for this line before the store flips — the effect
            // above reads the flag on the very next render.
            startedEditRef.current = true;
            beginEdit();
          }}
        >
          {settledText !== '' && <span data-role="ticket-detail-settled">{settledText}</span>}
          {tailText !== '' && (
            <span data-role="ticket-detail-tail" data-ghost="true">
              {tailText}
            </span>
          )}
          {micState === 'listening' && (
            <span data-role="ticket-detail-caret" aria-hidden="true">
              |
            </span>
          )}
        </button>
      )}
    </div>
  );
}
