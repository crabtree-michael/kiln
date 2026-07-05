import { describe, it, expect } from 'vitest';
import { decodeAssemblyMessage } from '@/voice/assemblyai-client';

describe('decodeAssemblyMessage', () => {
  it('Begin -> open', () => {
    expect(decodeAssemblyMessage(JSON.stringify({ type: 'Begin', id: 'x' }))).toEqual({
      kind: 'open',
    });
  });
  it('formatted end-of-turn -> final', () => {
    const msg = JSON.stringify({
      type: 'Turn',
      transcript: 'Hello.',
      end_of_turn: true,
      turn_is_formatted: true,
    });
    expect(decodeAssemblyMessage(msg)).toEqual({ kind: 'final', text: 'Hello.' });
  });
  it('mid-turn -> partial', () => {
    const msg = JSON.stringify({
      type: 'Turn',
      transcript: 'hello',
      end_of_turn: false,
      turn_is_formatted: false,
    });
    expect(decodeAssemblyMessage(msg)).toEqual({ kind: 'partial', text: 'hello' });
  });
  it('unformatted end-of-turn -> partial (wait for the formatted final)', () => {
    const msg = JSON.stringify({
      type: 'Turn',
      transcript: 'hello',
      end_of_turn: true,
      turn_is_formatted: false,
    });
    expect(decodeAssemblyMessage(msg)).toEqual({ kind: 'partial', text: 'hello' });
  });
  it('Error -> error (surfaces so the store recovers, not a silent dead socket)', () => {
    const msg = JSON.stringify({
      type: 'Error',
      error: 'Input Duration Violation',
      error_code: 3007,
    });
    expect(decodeAssemblyMessage(msg)).toEqual({ kind: 'error' });
  });
  it('garbage -> null', () => {
    expect(decodeAssemblyMessage('not json')).toBeNull();
    expect(decodeAssemblyMessage(JSON.stringify({ type: 'Nope' }))).toBeNull();
  });
});
