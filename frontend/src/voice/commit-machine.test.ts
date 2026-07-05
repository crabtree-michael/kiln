import { describe, it, expect } from 'vitest';
import { initialVoiceState, voiceReducer } from '@/voice/commit-machine';

describe('commit machine', () => {
  it('partials then formatted final -> armed pending, commit only after the grace window', () => {
    let s = initialVoiceState();
    s = voiceReducer(s, { type: 'provider', event: { kind: 'open' } });
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
    // The grace window closes with the send still armed -> exactly one commit.
    s = voiceReducer(s, { type: 'commitDelayElapsed' });
    expect(s.pending).toBeUndefined();
    expect(s.commit).toBe('Hello, world.');
    // next tick: a successful POST clears the transcript back to idle so stale
    // text can't linger (09 §4)
    s = voiceReducer(s, { type: 'commitConsumed' });
    expect(s.commit).toBeUndefined();
    expect(s.settledText).toBe('');
    expect(s.tailText).toBe('');
  });

  it('resumed speech within the grace window cancels the armed send', () => {
    let s = initialVoiceState();
    s = voiceReducer(s, { type: 'provider', event: { kind: 'open' } });
    s = voiceReducer(s, { type: 'provider', event: { kind: 'final', text: 'Hello there.' } });
    expect(s.pending).toBe('Hello there.');
    // A mid-thought pause was misread as end-of-turn; the user keeps talking.
    s = voiceReducer(s, { type: 'provider', event: { kind: 'partial', text: 'and one more thing' } });
    expect(s.pending).toBeUndefined(); // send cancelled
    expect(s.tailText).toBe('and one more thing');
    expect(s.micState).toBe('listening');
    // A late timer firing must not resurrect the cancelled send.
    s = voiceReducer(s, { type: 'commitDelayElapsed' });
    expect(s.commit).toBeUndefined();
  });

  it('commitDelayElapsed with nothing armed is a no-op', () => {
    let s = initialVoiceState();
    s = voiceReducer(s, { type: 'provider', event: { kind: 'open' } });
    s = voiceReducer(s, { type: 'commitDelayElapsed' });
    expect(s.commit).toBeUndefined();
    expect(s.pending).toBeUndefined();
  });

  it('failed commit keeps the finalized text on screen for a retry', () => {
    let s = initialVoiceState();
    s = voiceReducer(s, { type: 'provider', event: { kind: 'open' } });
    s = voiceReducer(s, { type: 'provider', event: { kind: 'final', text: 'Ship it.' } });
    s = voiceReducer(s, { type: 'commitDelayElapsed' }); // grace window closes -> commit
    expect(s.settledText).toBe('Ship it.');
    expect(s.commit).toBe('Ship it.');
    // POST failed: drop the one-tick commit but keep the ink visible (09 §4)
    s = voiceReducer(s, { type: 'commitFailed' });
    expect(s.commit).toBeUndefined();
    expect(s.settledText).toBe('Ship it.');
  });

  it('empty / whitespace final -> no commit', () => {
    let s = initialVoiceState();
    s = voiceReducer(s, { type: 'provider', event: { kind: 'open' } });
    s = voiceReducer(s, { type: 'provider', event: { kind: 'final', text: '   ' } });
    expect(s.commit).toBeUndefined();
  });

  it('X during tail -> no commit, tail cleared', () => {
    let s = initialVoiceState();
    s = voiceReducer(s, { type: 'provider', event: { kind: 'open' } });
    s = voiceReducer(s, { type: 'provider', event: { kind: 'partial', text: 'never mind' } });
    s = voiceReducer(s, { type: 'cancel' });
    expect(s.commit).toBeUndefined();
    expect(s.tailText).toBe('');
    expect(s.micState).toBe('listening');
  });

  it('socket drop -> retry, preserves un-committed transcript', () => {
    let s = initialVoiceState();
    s = voiceReducer(s, { type: 'provider', event: { kind: 'open' } });
    s = voiceReducer(s, { type: 'provider', event: { kind: 'partial', text: 'half a thought' } });
    s = voiceReducer(s, { type: 'providerFailed' }); // after the one silent reconnect already failed
    expect(s.micState).toBe('retry');
    expect(s.tailText).toBe('half a thought');
  });

  it('pause survives background/foreground', () => {
    let s = initialVoiceState();
    s = voiceReducer(s, { type: 'provider', event: { kind: 'open' } });
    s = voiceReducer(s, { type: 'pause' });
    expect(s.micState).toBe('paused');
    s = voiceReducer(s, { type: 'background' });
    s = voiceReducer(s, { type: 'foreground' });
    expect(s.micState).toBe('paused'); // explicit pause is sticky
  });

  it('foreground from an auto-stopped (background) listen resumes listening', () => {
    let s = initialVoiceState(); // starts listening
    s = voiceReducer(s, { type: 'provider', event: { kind: 'open' } });
    s = voiceReducer(s, { type: 'background' });
    expect(s.micState).toBe('listening'); // still the desired state; socket closed by the store
    s = voiceReducer(s, { type: 'foreground' });
    expect(s.micState).toBe('listening');
  });

  it('permission denied -> denied state', () => {
    let s = initialVoiceState();
    s = voiceReducer(s, { type: 'denied' });
    expect(s.micState).toBe('denied');
  });
});
