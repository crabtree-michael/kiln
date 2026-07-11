// NotFound behaviour: it names the missing page and offers a link home. Rendered
// under a MemoryRouter at an unmatched path so the test exercises the same
// catch-all wiring main.tsx uses, and confirms the home link points at `/`.
import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { NotFound } from '@/components/NotFound';

function renderAt(path: string): void {
  render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/" element={<div>home</div>} />
        <Route path="*" element={<NotFound />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe('NotFound', () => {
  it('renders on an unmatched route and states the page was not found', () => {
    renderAt('/does-not-exist');
    expect(screen.getByRole('heading', { name: 'Page not found' })).toBeInTheDocument();
  });

  it('offers a link back to the app root', () => {
    renderAt('/does-not-exist');
    expect(screen.getByRole('link', { name: 'Back to Kiln' })).toHaveAttribute('href', '/');
  });

  it('does not hijack a matched route', () => {
    renderAt('/');
    expect(screen.getByText('home')).toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Page not found' })).not.toBeInTheDocument();
  });
});
