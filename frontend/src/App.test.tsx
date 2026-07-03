import { render, screen } from '@testing-library/react';
import { App } from '@/App';

// Smoke test that keeps the frontend gate real: there is always at least one
// passing test so `pnpm test` is a meaningful green wall (02 §4a).
describe('App', () => {
  it('renders the Kiln heading', () => {
    render(<App />);
    expect(screen.getByRole('heading', { name: 'Kiln' })).toBeInTheDocument();
  });
});
