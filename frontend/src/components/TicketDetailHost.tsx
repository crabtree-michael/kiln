// The ticket sheet, wired (shell-architecture plan, layer L3). All three shells
// mounted the same fifteen-prop `<TicketDetail>` block, identical except for
// `placement`, including three hand-written close-after-action wrappers. It is
// written once here.
//
// **Per-action optionality is preserved, and it is a test seam rather than an
// accident.** Every `on*?` prop stays individually optional because that is how
// a presentational test omits one and asserts the affordance is ABSENT — roughly
// ten tests read that way. Collapsing the seven into a single required `actions`
// object would quietly change what all of them mean, so don't.
//
// **Which actions close the sheet is a product rule, not a detail.** Accept,
// Delete and Poke close: they end the visit, and what happens next comes back
// over the stream. The sandbox option, the two sandbox overrides and the text
// edit do NOT: those are things done *while reading* — a setting flipped, wording
// corrected, a wedged sandbox being watched — and after a correction the user
// should be looking at the corrected ticket rather than back at the feed.
import type { JSX } from 'react';
import { TicketDetail, type TicketTextEdit } from '@/components/TicketDetail';
import { TicketDetailTranscript } from '@/components/TicketDetailTranscript';
import { TicketDetailVoiceActions } from '@/components/TicketDetailVoiceActions';
import type { TicketOverlay } from '@/components/use-ticket-overlay';

export interface TicketDetailHostProps {
  /** The shell's overlay state — `useTicketOverlay()`'s return value, passed
   * whole. The host renders nothing when no ticket is open, so shells do not
   * need their own `openTicket !== null &&` guard. */
  overlay: TicketOverlay;
  /** Which edge the sheet is anchored to. The phone's bottom sheet is the
   * default; both desktop shells pass `'right'`, so the sheet reads as a panel
   * beside the work rather than a pop-up over it (13 D7a). */
  placement?: 'bottom' | 'right';
  onAccept: (ticketId: string) => void;
  onDelete?: ((ticketId: string) => void) | undefined;
  onPoke?: ((ticketId: string) => void) | undefined;
  onSetKeepSandbox?: ((ticketId: string, keep: boolean) => void) | undefined;
  onKillSandbox?: ((ticketId: string) => void) | undefined;
  onReassignSandbox?: ((ticketId: string) => void) | undefined;
  onEditText?: ((ticketId: string, patch: TicketTextEdit) => void) | undefined;
}

export function TicketDetailHost({
  overlay,
  placement = 'bottom',
  onAccept,
  onDelete,
  onPoke,
  onSetKeepSandbox,
  onKillSandbox,
  onReassignSandbox,
  onEditText,
}: TicketDetailHostProps): JSX.Element | null {
  const {
    openTicket,
    openAgentStatus,
    canReassign,
    openDependencies,
    closeTicket,
    voiceActive,
    setVoiceActive,
  } = overlay;
  if (openTicket === null) {
    return null;
  }
  return (
    <TicketDetail
      ticket={openTicket}
      // What this ticket waits for (0013), resolved by the overlay against the
      // board it already reads. Passing it here rather than from each shell is
      // the point of the host: one wiring, three shells.
      dependencies={openDependencies}
      surface="primary"
      placement={placement}
      // The sheet's voice cluster, shown on every ticket state — the unified
      // communication surface (08 §5) that replaced the old blocked-only "Talk to
      // unblock" button, so the user can start talking to the brain from any
      // ticket without leaving the sheet. Safe to always pass: it only mounts
      // (and only touches the voice store) when the sheet renders it.
      // `ticketTitle` registers this ticket with the voice store, so whatever is
      // sent from the sheet is prefixed with it and the brain knows what is being
      // commented on.
      voiceControl={
        <TicketDetailVoiceActions ticketTitle={openTicket.title} onActiveChange={setVoiceActive} />
      }
      voiceActive={voiceActive}
      // The live transcript for that mic, shown in the sheet's dock above the
      // controls so the user watches their words land without leaving the sheet.
      // Self-gating (renders nothing until there is text), so it is safe to
      // always pass.
      transcript={<TicketDetailTranscript />}
      onClose={closeTicket}
      // Accept only surfaces on a shaping proposal (TicketDetail gates it), so it
      // is safe to always wire — the sheet decides.
      onAccept={(ticketId) => {
        onAccept(ticketId);
        closeTicket();
      }}
      // Delete surfaces on a shaping proposal or a blocked ticket; Poke on
      // working/blocked. Both close after acting, like Accept. Left undefined
      // when the shell didn't wire them, so the affordance is absent — which is
      // what the presentational tests assert.
      onDelete={
        onDelete === undefined
          ? undefined
          : (ticketId) => {
              onDelete(ticketId);
              closeTicket();
            }
      }
      onPoke={
        onPoke === undefined
          ? undefined
          : (ticketId) => {
              onPoke(ticketId);
              closeTicket();
            }
      }
      // The four that leave the sheet open, passed straight through — see the
      // header for why each one is something done while reading rather than an
      // action that ends the visit.
      onSetKeepSandbox={onSetKeepSandbox}
      onKillSandbox={onKillSandbox}
      onReassignSandbox={onReassignSandbox}
      onEditText={onEditText}
      // Both from the board snapshot the shell already has, so the gear menu
      // describes the real sandbox rather than guessing from the ticket's column.
      sandboxStatus={openAgentStatus}
      canReassign={canReassign}
    />
  );
}
