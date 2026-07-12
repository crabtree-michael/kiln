// Dock DOM-structure snapshots (09 §8 image-snapshot targets: dock in Listening
// with live transcript, Paused, Denied, Retry). DOM-structure snapshots stand in
// for pixel snapshots, same deferral as the other components (07 §9 D4).
// `useVoice` is mocked to a fixed value per state so the markup is deterministic.
import { describe, it, expect, vi } from 'vitest';
import { render } from '@testing-library/react';
import { Dock } from '@/components/Dock';
import type { VoiceStoreValue } from '@/voice/voice-context';

let mockVoiceValue: VoiceStoreValue;

vi.mock('@/voice/voice-context', () => ({
  useVoice: (): VoiceStoreValue => mockVoiceValue,
}));

function stubVoice(overrides: Partial<VoiceStoreValue>): VoiceStoreValue {
  const noop = (): void => undefined;
  return {
    micState: 'listening',
    connecting: false,
    settledText: '',
    tailText: '',
    pause: noop,
    resume: noop,
    cancel: noop,
    sendNow: noop,
    countingDown: false,
    sendImminent: false,
    delaySend: noop,
    getLevel: () => 0,
    keyboardMode: false,
    openKeyboard: noop,
    closeKeyboard: noop,
    submitText: () => Promise.resolve(true),
    setTicketContext: noop,
    ...overrides,
  };
}

describe('Dock snapshots', () => {
  it('Listening with a live transcript (settled + ghosted tail + caret)', () => {
    mockVoiceValue = stubVoice({
      micState: 'listening',
      settledText: 'Ship the login screen.',
      tailText: 'and wire up',
    });
    const { container } = render(<Dock />);
    expect(container).toMatchSnapshot();
  });

  it('Listening with settled ink only (send + X flank the mic)', () => {
    mockVoiceValue = stubVoice({
      micState: 'listening',
      settledText: 'Ship the login screen.',
      tailText: '',
    });
    const { container } = render(<Dock />);
    expect(container).toMatchSnapshot();
  });

  it('Paused', () => {
    mockVoiceValue = stubVoice({ micState: 'paused' });
    const { container } = render(<Dock />);
    expect(container).toMatchSnapshot();
  });

  it('Denied', () => {
    mockVoiceValue = stubVoice({ micState: 'denied' });
    const { container } = render(<Dock />);
    expect(container).toMatchSnapshot();
  });

  it('Retry with an un-committed transcript preserved (09 §5)', () => {
    mockVoiceValue = stubVoice({ micState: 'retry', tailText: 'half a thought' });
    const { container } = render(<Dock />);
    expect(container).toMatchSnapshot();
  });

  it('Keyboard mode: typed field replaces the transcript, mic + dismiss + send flank the row', () => {
    mockVoiceValue = stubVoice({ keyboardMode: true });
    const { container } = render(<Dock />);
    expect(container).toMatchSnapshot();
  });
});
