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
    // the mic stops (drops to Paused) rather than listening on into the next turn.
    s = voiceReducer(s, { type: 'commitDelayElapsed' });
    expect(s.pending).toBeUndefined();
    expect(s.commit).toBe('Hello, world.');
    expect(s.micState).toBe('paused');
    // next tick: a successful POST clears the transcript back to idle so stale
    // text can't linger (09 §4); the mic stays off until the next tap.
    s = voiceReducer(s, { type: 'commitConsumed' });
    expect(s.commit).toBeUndefined();
    expect(s.settledText).toBe('');
    expect(s.tailText).toBe('');
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
    // mic stops (Paused) rather than listening on.
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
    expect(s.micState).toBe('paused');
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
    // Nothing was sent, so the mic keeps listening.
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
    s = voiceReducer(s, { type: 'commitDelayElapsed' }); // grace window closes -> commit + Paused
    expect(s.settledText).toBe('Ship it.');
    expect(s.commit).toBe('Ship it.');
    expect(s.micState).toBe('paused');
    // POST failed: drop the one-tick commit but keep the ink visible (09 §4). The
    // mic stays off — the user re-taps (or taps send again) to retry.
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
});
