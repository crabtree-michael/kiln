// The dock — VISUAL SHELL ONLY (08 §F, project decision): the mic mark and a
// tap-to-talk button reproduced from the design, but non-functional. Real STT /
// utterance commit lands in spec 09; here the button exists so the layout and
// the `getByRole('button', { name: 'Talk' })` selector are in place. Always
// `data-dock-state="idle"` for now.
import type { JSX } from 'react';

export function Dock(): JSX.Element {
  return (
    <div data-role="dock" data-dock-state="idle">
      <button type="button" data-role="dock-talk" aria-label="Talk">
        <span data-role="dock-mic" aria-hidden="true">
          <span data-role="dock-mic-capsule" />
          <span data-role="dock-mic-arc" />
          <span data-role="dock-mic-stem" />
        </span>
        <span data-role="dock-label">Tap to talk</span>
      </button>
    </div>
  );
}
