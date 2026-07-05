// cardSummary: the compact proposal-card digest (08 §5). The full shaped body
// lives behind the click-through; here we assert the feed snippet collapses
// whitespace and truncates long bodies while passing short ones through whole.
import { describe, expect, it } from 'vitest';
import { cardSummary } from '@/components/feed-format';

describe('cardSummary', () => {
  it('passes a short body through unchanged', () => {
    expect(cardSummary('Rework the login screen to a single column.')).toBe(
      'Rework the login screen to a single column.',
    );
  });

  it('collapses newlines and runs of whitespace into single spaces', () => {
    expect(cardSummary('First paragraph.\n\nSecond   paragraph.\tThird.')).toBe(
      'First paragraph. Second paragraph. Third.',
    );
  });

  it('truncates a long body to the budget and appends an ellipsis', () => {
    const body = 'x'.repeat(400);
    const summary = cardSummary(body);
    expect(summary.endsWith('…')).toBe(true);
    // 200 chars of body + the ellipsis.
    expect(summary).toHaveLength(201);
    expect(summary.length).toBeLessThan(body.length);
  });

  it('honours a custom max length', () => {
    expect(cardSummary('abcdefghij', 4)).toBe('abcd…');
  });

  it('keeps a short trailing marker visible (E2E hasText contract)', () => {
    // The proposal E2E filters the card by a tag appended to a ~85-char body;
    // the 200-char budget must keep that whole body — tag included — visible.
    const body = 'Trust the session cookie; use the JWT only for the mobile clients. E2E-ABC123';
    expect(cardSummary(body)).toContain('E2E-ABC123');
  });
});
