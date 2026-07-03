// Placeholder shell. The real client renders the board over a live connection,
// captures mic audio, plays Kiln's voice and handles notifications (02 §11).
// It holds no authoritative state. This file exists so the harness — typecheck,
// lint and test — has real code to gate on before the surface area is built.
import type { JSX } from 'react';

export function App(): JSX.Element {
  return (
    <main>
      <h1>Kiln</h1>
      <p>Harness online. Board client to follow (docs/specs/02 §11).</p>
    </main>
  );
}
