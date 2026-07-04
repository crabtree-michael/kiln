// Agent notifications panel (debug view): renders the brain-authored
// update/preview notification rows in their own panel, with an all-clear empty
// state. Presentational — fed already-filtered cards, so these assertions never
// touch the feed store or transport.
import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { NotificationsPanel } from '@/components/NotificationsPanel';
import { makeFeedCard } from '@/test/fixtures';

// A fixed "now" keeps the relative-age text deterministic.
const NOW = new Date('2026-07-04T00:10:00Z').getTime();

describe('NotificationsPanel', () => {
  it('shows the empty state when there are no notifications', () => {
    render(<NotificationsPanel notifications={[]} now={NOW} />);

    expect(screen.getByText('No agent notifications yet.')).toBeInTheDocument();
    expect(screen.queryByRole('listitem')).toBeNull();
  });

  it('renders each notification with its label, kind tag, and body', () => {
    const notifications = [
      makeFeedCard({
        kind: 'update',
        id: 'update:2',
        label: 'Widget shipped',
        body: 'The widget ticket is done.',
        createdAt: '2026-07-04T00:08:00Z',
        notificationId: 2,
      }),
      makeFeedCard({
        kind: 'preview',
        id: 'update:1',
        label: 'Landing page',
        body: 'Have a look at the new hero.',
        createdAt: '2026-07-04T00:00:00Z',
        notificationId: 1,
        imageUrl: 'https://example.test/hero.png',
      }),
    ];

    render(<NotificationsPanel notifications={notifications} now={NOW} />);

    const items = screen.getAllByRole('listitem');
    expect(items).toHaveLength(2);
    expect(items.map((item) => item.getAttribute('data-kind'))).toEqual(['update', 'preview']);

    expect(screen.getByText('Widget shipped')).toBeInTheDocument();
    expect(screen.getByText('Update')).toBeInTheDocument();
    expect(screen.getByText('Preview')).toBeInTheDocument();
    expect(screen.getByText('The widget ticket is done.')).toBeInTheDocument();
  });

  it('renders the preview image only for preview cards that carry one', () => {
    const notifications = [
      makeFeedCard({
        kind: 'update',
        id: 'update:2',
        label: 'No image',
        body: 'text only',
        createdAt: '2026-07-04T00:08:00Z',
        notificationId: 2,
      }),
      makeFeedCard({
        kind: 'preview',
        id: 'update:1',
        label: 'Landing page',
        body: 'with image',
        createdAt: '2026-07-04T00:00:00Z',
        notificationId: 1,
        imageUrl: 'https://example.test/hero.png',
      }),
    ];

    render(<NotificationsPanel notifications={notifications} now={NOW} />);

    const images = screen.getAllByRole('img');
    expect(images).toHaveLength(1);
    expect(images[0]).toHaveAttribute('src', 'https://example.test/hero.png');
  });
});
