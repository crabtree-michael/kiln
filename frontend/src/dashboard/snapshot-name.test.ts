// The captured-snapshot name. Its shape is a contract shared with the server's
// own derivation (backend/cmd/kiln/adapters.go), not a cosmetic — a snapshot
// taken from this form and one taken when a ticket finishes sit in the same
// picker and must read the same way.
import { describe, expect, it } from 'vitest';
import { snapshotNameFor } from '@/dashboard/snapshot-name';

// The instant behind Pac-Man's hand-taken `pacman-20260809141304`, the name this
// shape is pinned to.
const at = new Date(Date.UTC(2026, 7, 9, 14, 13, 4));

describe('snapshotNameFor', () => {
  it('names a snapshot <project>-YYYYMMDDHHMMSS', () => {
    expect(snapshotNameFor('pacman', at)).toBe('pacman-20260809141304');
  });

  it('slugs the project name, since it is free text becoming an identifier', () => {
    // The stem is the project's name reduced, not reproduced: a board called
    // "Pac-Man" keeps its dash, and one called "Acme Widgets" gains one.
    expect(snapshotNameFor('Pac-Man', at)).toBe('pac-man-20260809141304');
    expect(snapshotNameFor('  Acme//Widgets!  ', at)).toBe('acme-widgets-20260809141304');
  });

  it('falls back to a stem when the name slugs to nothing', () => {
    expect(snapshotNameFor('***', at)).toBe('kiln-20260809141304');
    expect(snapshotNameFor('', at)).toBe('kiln-20260809141304');
  });

  it('reads the clock in UTC, so it matches a name the server derived', () => {
    // The same instant, expressed from a zone eight hours behind. A local-time
    // reading would name this one 20260809061304 and sort it wrong beside the
    // server's.
    expect(snapshotNameFor('pacman', new Date(at.getTime()))).toBe('pacman-20260809141304');
    expect(snapshotNameFor('pacman', new Date('2026-08-09T06:13:04-08:00'))).toBe(
      'pacman-20260809141304',
    );
  });

  it('pads every field but the year, so names sort lexically by age', () => {
    expect(snapshotNameFor('pacman', new Date(Date.UTC(2026, 0, 2, 3, 4, 5)))).toBe(
      'pacman-20260102030405',
    );
  });
});
