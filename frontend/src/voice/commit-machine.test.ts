import { describe, it, expect } from 'vitest';
import { initialVoiceState, voiceReducer } from '@/voice/commit-machine';

describe('commit machine', () => {
  it('partials then formatted final -> exactly one commit', () => {
    let s = initialVoiceState();
    s = voiceReducer(s, { type: 'provider', event: { kind: 'open' } });
    s = voiceReducer(s, { type: 'provider', event: { kind: 'partial', text: 'hello wor' } });
    expect(s.tailText).toBe('hello wor');
    expect(s.commit).toBeUndefined();
    s = voiceReducer(s, { type: 'provider', event: { kind: 'partial', text: 'hello world' } });
    s = voiceReducer(s, { type: 'provider', event: { kind: 'final', text: 'Hello, world.' } });
    expect(s.commit).toBe('Hello, world.');
    expect(s.settledText).toBe('Hello, world.');
    expect(s.tailText).toBe('');
    // next tick: a successful POST clears the transcript back to idle so stale
    // text can't linger (09 §4)
    s = voiceReducer(s, { type: 'commitConsumed' });
    expect(s.commit).toBeUndefined();
    expect(s.settledText).toBe('');
    expect(s.tailText).toBe('');
  });

  it('failed commit keeps the finalized text on screen for a retry', () => {
    let s = initialVoiceState();
    s = voiceReducer(s, { type: 'provider', event: { kind: 'open' } });
    s = voiceReducer(s, { type: 'provider', event: { kind: 'final', text: 'Ship it.' } });
    expect(s.settledText).toBe('Ship it.');
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
