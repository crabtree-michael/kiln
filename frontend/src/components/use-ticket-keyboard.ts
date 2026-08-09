// The ticket sheet's typed-input state (08 §5, 09 §4): the keyboard half of the
// sheet's mic-first dock, held in ONE place because it is rendered in TWO.
//
// The sheet's dock is two slots — the panel above (`TicketDetail`'s `transcript`
// node) and the controls row below (its `voiceControl` node) — and typing needs
// both: the field belongs in the panel, where the spoken transcript lands, and
// its Send and × belong in the row beside the mic, where the spoken utterance's
// Send and × already are. Neither component can own the draft, so the host that
// builds both nodes owns it and hands each the same object.
//
// **It is deliberately NOT the voice store's `keyboardMode`.** That flag is the
// DOCK's typing mode, and flipping it from the sheet would reach through the
// scrim: the dock would enter the mode with a second, empty draft of its own,
// race the sheet for the caret (both focus their field the moment it opens), and
// outlive the sheet that opened it. Two surfaces with two drafts get two flags.
// What they share is the seam underneath — `submitText`, the same
// `POST /api/message` a spoken utterance rides, already prefixed with the open
// ticket's title (`setTicketContext`, registered by the sheet's own `MicButton`),
// so a typed comment reaches the brain with exactly the context a spoken one does.
//
// Requires a `VoiceProvider` ancestor, like every other consumer in the sheet's
// dock — `TicketDetailHost` is only ever mounted inside one.
import { useCallback, useEffect, useRef, useState, type RefObject } from 'react';
import { useVoice } from '@/voice/voice-context';

export interface TicketKeyboard {
  /** Whether the sheet is in typed-input mode: the dock's panel is a field and
   *  its controls row carries Send and × for the draft rather than for an
   *  utterance. */
  open: boolean;
  /** What has been typed so far. Pure view state — the store only sees it on
   *  submit, so keystrokes never churn the voice machine's transcript. */
  draft: string;
  /** The field itself, so the row's controls can put the caret back after a send
   *  and the panel can focus it the moment the mode opens. */
  inputRef: RefObject<HTMLTextAreaElement>;
  setDraft: (text: string) => void;
  /** Open typed input: stop the mic and drop any un-committed transcript, so a
   *  spoken and a typed message can never go out as one. */
  begin: () => void;
  /** Leave typed input, discarding the draft — what the sheet's × does in this
   *  mode, mirroring the way that × is the way *out* of the speaking one. */
  end: () => void;
  /** Post the draft. Clears the field only on a successful POST; on failure the
   *  text stays so the user can retry (the commit effect's no-modal stance,
   *  09 §4). Stays in the mode after a send, so consecutive messages can be typed. */
  submit: () => void;
}

export function useTicketKeyboard(ticketId: string | null): TicketKeyboard {
  const { micState, pause, cancel, submitText } = useVoice();
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState('');
  const inputRef = useRef<HTMLTextAreaElement | null>(null);

  const end = useCallback((): void => {
    setOpen(false);
    setDraft('');
  }, []);

  const begin = useCallback((): void => {
    // One input at a time: the mic stops and whatever it had heard goes with it.
    // (The store's `openKeyboard` does exactly this for the dock; the sheet does
    // it here, without the shared flag — see the header.)
    pause();
    cancel();
    setDraft('');
    setOpen(true);
  }, [pause, cancel]);

  const submit = useCallback((): void => {
    const text = draft.trim();
    if (text === '') {
      return;
    }
    void submitText(text).then((sent) => {
      if (sent) {
        setDraft('');
      }
    });
  }, [draft, submitText]);

  // The mic going live is the way back to voice. The orb never leaves the sheet's
  // footer — it is the one control that must not move (see
  // `TicketDetailVoiceActions`) — so tapping it IS this mode's "switch to voice"
  // button, and typed input has to stand down for it. Nothing else can start the
  // mic under an open field, since `begin` paused it — and that pause is
  // synchronous, landing in the same React batch as the mode opening, so this can
  // never fire on the render that opens the field.
  useEffect(() => {
    if (!open || micState !== 'listening') {
      return;
    }
    end();
  }, [open, micState, end]);

  // The draft is scoped to the ticket it comments on — `submitText` prefixes it
  // with that ticket's title — so it must not survive the sheet closing (`null`)
  // or a deep link swapping the open ticket underneath it. Mirrors the transcript
  // line's unmount cleanup: this host stays mounted between visits, so nothing
  // else would clear it.
  useEffect(() => {
    setOpen(false);
    setDraft('');
  }, [ticketId]);

  return { open, draft, inputRef, setDraft, begin, end, submit };
}
