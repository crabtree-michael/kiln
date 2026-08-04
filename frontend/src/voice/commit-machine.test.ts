import { describe, it, expect } from 'vitest';
import { initialVoiceState, voiceReducer, type VoiceState } from '@/voice/commit-machine';

// The app opens Paused (mic off until an explicit tap): while paused the machine
// ignores all provider chatter, so nothing transcribes on its own. A tap →
// `resume` (→ listening), then the socket's `open` confirms it. Every test that
// exercises live transcription starts from that tapped-on state.
function listening(): VoiceState {
  let s = initialVoiceState();
  s = voiceReducer(s, { type: 'resume' });
  s = voiceReducer(s, { type: 'provider', event: { kind: 'open' } });
  return s;
}

describe('commit machine', () => {
  it('opens Paused — the mic is off until an explicit tap (no auto-listen)', () => {
    const s = initialVoiceState();
    expect(s.micState).toBe('paused');
    // While paused, provider events are inert: even an `open` can't flip it to
    // listening — only a user `resume` (the mic tap) does.
    const afterOpen = voiceReducer(s, { type: 'provider', event: { kind: 'open' } });
    expect(afterOpen.micState).toBe('paused');
    const afterTap = voiceReducer(s, { type: 'resume' });
    expect(afterTap.micState).toBe('listening');
  });

  it('resume marks connecting until the socket opens, then clears it', () => {
    let s = initialVoiceState();
    expect(s.connecting).toBe(false);
    // The mic tap flips to listening immediately, but the socket is still coming
    // up: `connecting` is set so the dock shows a spinner, not the live glow.
    s = voiceReducer(s, { type: 'resume' });
    expect(s.micState).toBe('listening');
    expect(s.connecting).toBe(true);
    // The socket opens -> recording is actually live -> spinner clears.
    s = voiceReducer(s, { type: 'provider', event: { kind: 'open' } });
    expect(s.connecting).toBe(false);
  });

  it('stopping the mic during the setup window clears connecting', () => {
    // pause, denied, background, and providerFailed all end the setup window
    // without ever reaching a live socket — none should leave the spinner armed.
    const resumed = (): VoiceState => voiceReducer(initialVoiceState(), { type: 'resume' });
    expect(voiceReducer(resumed(), { type: 'pause' }).connecting).toBe(false);
    expect(voiceReducer(resumed(), { type: 'denied' }).connecting).toBe(false);
    expect(voiceReducer(resumed(), { type: 'background' }).connecting).toBe(false);
    expect(voiceReducer(resumed(), { type: 'providerFailed' }).connecting).toBe(false);
  });

  it('partials then formatted final -> armed pending, commit only after the grace window', () => {
    let s = listening();
    s = voiceReducer(s, { type: 'provider', event: { kind: 'partial', text: 'hello wor' } });
    expect(s.tailText).toBe('hello wor');
    expect(s.pending).toBeUndefined();
    expect(s.commit).toBeUndefined();
    s = voiceReducer(s, { type: 'provider', event: { kind: 'partial', text: 'hello world' } });
    s = voiceReducer(s, { type: 'provider', event: { kind: 'final', text: 'Hello, world.' } });
    // The final arms the send but holds it — nothing POSTs yet (09 §4).
    expect(s.pending).toBe('Hello, world.');
    expect(s.commit).toBeUndefined();
    expect(s.settledText).toBe('Hello, world.');
    expect(s.tailText).toBe('');
    // The grace window closes with the send still armed -> exactly one commit, and
    // the send RELEASES the mic (→ Paused) so the store can tear the audio session
    // down and other apps' audio can resume (09 §3a) — no hands-free continuation.
    s = voiceReducer(s, { type: 'commitDelayElapsed' });
    expect(s.pending).toBeUndefined();
    expect(s.commit).toBe('Hello, world.');
    expect(s.micState).toBe('paused');
    // next tick: a successful POST clears the transcript back to idle so stale
    // text can't linger (09 §4); the mic stays Paused (tap to talk for the next).
    s = voiceReducer(s, { type: 'commitConsumed' });
    expect(s.commit).toBeUndefined();
    expect(s.settledText).toBe('');
    expect(s.tailText).toBe('');
    expect(s.micState).toBe('paused');
  });

  it('auto-send releases the mic (→ Paused) so the audio session can free other apps audio', () => {
    let s = listening();
    // Speak, end-of-turn final, grace window closes -> auto-send.
    s = voiceReducer(s, { type: 'provider', event: { kind: 'final', text: 'First.' } });
    s = voiceReducer(s, { type: 'commitDelayElapsed' });
    expect(s.commit).toBe('First.');
    // The send drops to Paused: the store tears the stream down, ending the
    // play-and-record session so iOS can resume music/podcasts (09 §3a). No more
    // hands-free continuation across turns; the user taps to talk for the next.
    expect(s.micState).toBe('paused');
    s = voiceReducer(s, { type: 'commitConsumed' });
    expect(s.settledText).toBe('');
    expect(s.micState).toBe('paused');
  });

  it('resumed speech within the grace window cancels the armed send', () => {
    let s = listening();
    s = voiceReducer(s, { type: 'provider', event: { kind: 'final', text: 'Hello there.' } });
    expect(s.pending).toBe('Hello there.');
    // A mid-thought pause was misread as end-of-turn; the user keeps talking.
    s = voiceReducer(s, {
      type: 'provider',
      event: { kind: 'partial', text: 'and one more thing' },
    });
    expect(s.pending).toBeUndefined(); // send cancelled
    expect(s.tailText).toBe('and one more thing');
    expect(s.micState).toBe('listening');
    // A late timer firing must not resurrect the cancelled send.
    s = voiceReducer(s, { type: 'commitDelayElapsed' });
    expect(s.commit).toBeUndefined();
  });

  it('a final after resumed speech appends to the pending settled text, not replaces it', () => {
    let s = listening();
    s = voiceReducer(s, { type: 'provider', event: { kind: 'final', text: 'Hello there.' } });
    expect(s.settledText).toBe('Hello there.');
    expect(s.pending).toBe('Hello there.');
    // The user keeps talking in the grace window: a partial cancels the armed
    // send but leaves the first final's ink on screen.
    s = voiceReducer(s, {
      type: 'provider',
      event: { kind: 'partial', text: 'and one more thing' },
    });
    expect(s.pending).toBeUndefined();
    expect(s.settledText).toBe('Hello there.');
    // The continued speech finalizes: the growing transcript keeps the first
    // final rather than discarding it, and the whole thing is armed to send.
    s = voiceReducer(s, {
      type: 'provider',
      event: { kind: 'final', text: 'And one more thing.' },
    });
    expect(s.settledText).toBe('Hello there. And one more thing.');
    expect(s.pending).toBe('Hello there. And one more thing.');
    expect(s.tailText).toBe('');
    // The grace window closes -> exactly one commit carries the full utterance.
    s = voiceReducer(s, { type: 'commitDelayElapsed' });
    expect(s.commit).toBe('Hello there. And one more thing.');
  });

  it('sendNow commits the on-screen final immediately, skipping the grace window', () => {
    let s = listening();
    s = voiceReducer(s, { type: 'provider', event: { kind: 'final', text: 'Ship it.' } });
    expect(s.pending).toBe('Ship it.');
    expect(s.commit).toBeUndefined();
    // The user taps send before the window elapses -> commit right away, and the
    // send RELEASES the mic (→ Paused) so the audio session ends and other apps'
    // audio can resume (09 §3a) rather than reopening a socket for more speech.
    s = voiceReducer(s, { type: 'sendNow' });
    expect(s.pending).toBeUndefined();
    expect(s.commit).toBe('Ship it.');
    expect(s.micState).toBe('paused');
  });

  it('sendNow commits the interim tail without waiting for a final', () => {
    let s = listening();
    s = voiceReducer(s, {
      type: 'provider',
      event: { kind: 'partial', text: 'buy milk and eggs' },
    });
    // No final has landed — only a ghosted tail — yet send fires what is shown.
    expect(s.pending).toBeUndefined();
    s = voiceReducer(s, { type: 'sendNow' });
    expect(s.commit).toBe('buy milk and eggs');
    expect(s.settledText).toBe('buy milk and eggs');
    expect(s.tailText).toBe('');
    // The send releases the mic (→ Paused) with no setup spinner armed: the turn
    // ends so the audio session can free other apps' audio (09 §3a).
    expect(s.micState).toBe('paused');
    expect(s.connecting).toBe(false);
  });

  it('sendNow commits settled ink + a resumed tail as one utterance', () => {
    let s = listening();
    s = voiceReducer(s, { type: 'provider', event: { kind: 'final', text: 'Ship it.' } });
    s = voiceReducer(s, { type: 'provider', event: { kind: 'partial', text: 'and deploy' } });
    // A final settled, then the user kept talking (tail) — send takes both.
    s = voiceReducer(s, { type: 'sendNow' });
    expect(s.commit).toBe('Ship it. and deploy');
  });

  it('sendNow with nothing on screen is a no-op (and does not stop the mic)', () => {
    let s = listening();
    s = voiceReducer(s, { type: 'sendNow' });
    expect(s.commit).toBeUndefined();
    expect(s.pending).toBeUndefined();
    // Nothing was sent, so the mic keeps listening — a send is what releases it.
    expect(s.micState).toBe('listening');
  });

  it('stopping the mic (pause) only stops — it does not auto-send', () => {
    let s = listening();
    s = voiceReducer(s, { type: 'provider', event: { kind: 'final', text: 'Ship it.' } });
    expect(s.pending).toBe('Ship it.');
    // Tapping the mic to stop just pauses; the armed send is dropped (its timer
    // must not fire while paused), but the finalized text stays on screen so the
    // user can still send it (send button) or clear it (X).
    s = voiceReducer(s, { type: 'pause' });
    expect(s.micState).toBe('paused');
    expect(s.pending).toBeUndefined();
    expect(s.commit).toBeUndefined();
    expect(s.settledText).toBe('Ship it.');
  });

  it('stopping the mic preserves a still-forming tail (the "stuck" case)', () => {
    let s = listening();
    s = voiceReducer(s, { type: 'provider', event: { kind: 'partial', text: 'half a thought' } });
    // Stopping mid-utterance leaves the interim on screen (the socket is closed,
    // so it can no longer finalize) — the user sends or clears it manually.
    s = voiceReducer(s, { type: 'pause' });
    expect(s.micState).toBe('paused');
    expect(s.commit).toBeUndefined();
    expect(s.tailText).toBe('half a thought');
  });

  it('commitDelayElapsed with nothing armed is a no-op', () => {
    let s = listening();
    s = voiceReducer(s, { type: 'commitDelayElapsed' });
    expect(s.commit).toBeUndefined();
    expect(s.pending).toBeUndefined();
    // A stray timer with nothing armed must not stop a live listen.
    expect(s.micState).toBe('listening');
  });

  it('failed commit keeps the finalized text on screen for a retry', () => {
    let s = listening();
    s = voiceReducer(s, { type: 'provider', event: { kind: 'final', text: 'Ship it.' } });
    s = voiceReducer(s, { type: 'commitDelayElapsed' }); // grace window closes -> commit; mic released to Paused
    expect(s.settledText).toBe('Ship it.');
    expect(s.commit).toBe('Ship it.');
    expect(s.micState).toBe('paused');
    // POST failed: drop the one-tick commit but keep the ink visible (09 §4). The
    // send already released the mic to Paused; the failed text stays on screen and
    // the user taps to talk to retry.
    s = voiceReducer(s, { type: 'commitFailed' });
    expect(s.commit).toBeUndefined();
    expect(s.settledText).toBe('Ship it.');
    expect(s.micState).toBe('paused');
  });

  it('empty / whitespace final -> no commit', () => {
    let s = listening();
    s = voiceReducer(s, { type: 'provider', event: { kind: 'final', text: '   ' } });
    expect(s.commit).toBeUndefined();
  });

  it('X during tail -> no commit, tail cleared', () => {
    let s = listening();
    s = voiceReducer(s, { type: 'provider', event: { kind: 'partial', text: 'never mind' } });
    s = voiceReducer(s, { type: 'cancel' });
    expect(s.commit).toBeUndefined();
    expect(s.tailText).toBe('');
    expect(s.micState).toBe('listening');
  });

  it('X clears settled ink too (wipes a frozen "stuck" transcript)', () => {
    let s = listening();
    s = voiceReducer(s, { type: 'provider', event: { kind: 'final', text: 'Ship it.' } });
    s = voiceReducer(s, { type: 'pause' }); // stop listening -> settled ink frozen on screen
    expect(s.settledText).toBe('Ship it.');
    s = voiceReducer(s, { type: 'cancel' });
    expect(s.settledText).toBe('');
    expect(s.commit).toBeUndefined();
  });

  it('socket drop -> retry, preserves un-committed transcript', () => {
    let s = listening();
    s = voiceReducer(s, { type: 'provider', event: { kind: 'partial', text: 'half a thought' } });
    s = voiceReducer(s, { type: 'providerFailed' }); // after the one silent reconnect already failed
    expect(s.micState).toBe('retry');
    expect(s.tailText).toBe('half a thought');
  });

  it('backgrounding stops a live listen — it drops to Paused and never auto-resumes', () => {
    let s = listening();
    expect(s.micState).toBe('listening');
    // Leaving the app closes the socket (store) and the machine drops to Paused;
    // there is no `foreground` action that re-opens it — the user taps to talk again.
    s = voiceReducer(s, { type: 'background' });
    expect(s.micState).toBe('paused');
  });

  it('an explicit pause is unaffected by backgrounding (stays paused)', () => {
    let s = listening();
    s = voiceReducer(s, { type: 'pause' });
    expect(s.micState).toBe('paused');
    s = voiceReducer(s, { type: 'background' });
    expect(s.micState).toBe('paused');
  });

  it('permission denied -> denied state', () => {
    let s = initialVoiceState();
    s = voiceReducer(s, { type: 'denied' });
    expect(s.micState).toBe('denied');
  });

  // Correcting the transcript before it goes (09 §4a). The rule that makes this
  // more than a text field: the armed end-of-turn send SURVIVES the edit, pointed
  // at the corrected words — it is the store that freezes its countdown, so the
  // machine must never quietly drop `pending` here the way `pause`/`cancel` do.
  describe('editing the transcript before it sends', () => {
    it('beginEdit folds the still-forming tail into the ink and stops the mic', () => {
      let s = listening();
      s = voiceReducer(s, { type: 'provider', event: { kind: 'final', text: 'Ship the login.' } });
      s = voiceReducer(s, { type: 'provider', event: { kind: 'partial', text: 'and the' } });
      s = voiceReducer(s, { type: 'beginEdit' });
      // One line of editable text, nothing left ghosted or still to arrive.
      expect(s.settledText).toBe('Ship the login. and the');
      expect(s.tailText).toBe('');
      // The mic is released so fresh words can't land in the line being corrected.
      expect(s.micState).toBe('paused');
      expect(s.connecting).toBe(false);
    });

    it('beginEdit KEEPS the armed send, re-pointed at the folded text', () => {
      let s = listening();
      s = voiceReducer(s, { type: 'provider', event: { kind: 'final', text: 'Ship the login.' } });
      expect(s.pending).toBe('Ship the login.');
      s = voiceReducer(s, { type: 'beginEdit' });
      // The crux: unlike `pause` (which disarms), the send is still armed — the
      // store merely holds its countdown while the field has focus.
      expect(s.pending).toBe('Ship the login.');
      expect(s.commit).toBeUndefined();
    });

    it('beginEdit is a no-op with nothing on screen — an empty field never stops a live mic', () => {
      const s = listening();
      const after = voiceReducer(s, { type: 'beginEdit' });
      expect(after).toBe(s);
      expect(after.micState).toBe('listening');
    });

    it('an edit re-points the armed send at what the user actually wrote', () => {
      let s = listening();
      s = voiceReducer(s, { type: 'provider', event: { kind: 'final', text: 'Ship the log in.' } });
      s = voiceReducer(s, { type: 'beginEdit' });
      s = voiceReducer(s, { type: 'editTranscript', text: 'Ship the login screen.' });
      expect(s.settledText).toBe('Ship the login screen.');
      expect(s.pending).toBe('Ship the login screen.');
      // And that is what fires when the store's countdown resumes.
      s = voiceReducer(s, { type: 'commitDelayElapsed' });
      expect(s.commit).toBe('Ship the login screen.');
    });

    it('an edit holds the text verbatim, so a trailing space mid-word survives', () => {
      let s = listening();
      s = voiceReducer(s, { type: 'provider', event: { kind: 'final', text: 'Ship it.' } });
      s = voiceReducer(s, { type: 'editTranscript', text: 'Ship it and ' });
      expect(s.settledText).toBe('Ship it and ');
      // What POSTs is trimmed even though what shows is not.
      expect(s.pending).toBe('Ship it and');
    });

    it('editing the transcript down to nothing disarms the send', () => {
      let s = listening();
      s = voiceReducer(s, { type: 'provider', event: { kind: 'final', text: 'Ship it.' } });
      s = voiceReducer(s, { type: 'editTranscript', text: '   ' });
      // Empty utterances never post (09 §4) — there is no countdown left to resume.
      expect(s.pending).toBeUndefined();
      s = voiceReducer(s, { type: 'commitDelayElapsed' });
      expect(s.commit).toBeUndefined();
    });

    it('typing into an unarmed transcript does not arm a send of its own', () => {
      let s = listening();
      // Nothing has ended a turn, so nothing is armed: typing is just typing.
      s = voiceReducer(s, { type: 'editTranscript', text: 'a typed thought' });
      expect(s.settledText).toBe('a typed thought');
      expect(s.pending).toBeUndefined();
      // Typing is a handover in its own right, so it releases the mic too.
      expect(s.micState).toBe('paused');
    });

    it('the send button commits the corrected text, not the heard text', () => {
      let s = listening();
      s = voiceReducer(s, { type: 'provider', event: { kind: 'final', text: 'Ship the log in.' } });
      s = voiceReducer(s, { type: 'beginEdit' });
      s = voiceReducer(s, { type: 'editTranscript', text: 'Ship the login screen.' });
      s = voiceReducer(s, { type: 'sendNow' });
      expect(s.commit).toBe('Ship the login screen.');
      expect(s.micState).toBe('paused');
    });

    it('the X still wipes an edit in progress', () => {
      let s = listening();
      s = voiceReducer(s, { type: 'provider', event: { kind: 'final', text: 'Ship it.' } });
      s = voiceReducer(s, { type: 'beginEdit' });
      s = voiceReducer(s, { type: 'editTranscript', text: 'Ship it tomorrow.' });
      s = voiceReducer(s, { type: 'cancel' });
      expect(s.settledText).toBe('');
      expect(s.pending).toBeUndefined();
      expect(s.commit).toBeUndefined();
    });
  });
});
