// The one derivation behind "a new project is named after its repository"
// (auto-name from repository). Three surfaces read it, so the edge cases live
// here rather than being re-argued in each of their component tests.
import { describe, expect, it } from 'vitest';
import { projectNameFromRepoUrl } from '@/dashboard/project-name';

describe('projectNameFromRepoUrl', () => {
  it('takes the repo’s own name, not the org or the path', () => {
    expect(projectNameFromRepoUrl('https://github.com/Crabtree-Michael/Pac-Man')).toBe('Pac-Man');
  });

  it('preserves the repo’s casing and punctuation', () => {
    // The board is called what GitHub calls the repo — this is a label a person
    // reads, so lower-casing or de-hyphenating it would be a wrong answer.
    expect(projectNameFromRepoUrl('https://github.com/acme/My_Thing.v2')).toBe('My_Thing.v2');
  });

  // The three spellings hand-entered repo_urls carry (the same set `sameRepo`
  // normalizes) — a stored value can reach this on any surface that seeds a
  // picker from one.
  it.each([
    ['https://github.com/acme/demo', 'demo'],
    ['https://github.com/acme/demo/', 'demo'],
    ['https://github.com/acme/demo.git', 'demo'],
    ['  https://github.com/acme/demo.git  ', 'demo'],
    ['https://GitHub.com/Acme/Demo', 'Demo'],
  ])('normalizes %s to %s', (url, expected) => {
    expect(projectNameFromRepoUrl(url)).toBe(expected);
  });

  it('answers empty for no repo, which is how the create flows read "not picked yet"', () => {
    expect(projectNameFromRepoUrl('')).toBe('');
    expect(projectNameFromRepoUrl('   ')).toBe('');
  });

  it('is not confused by a non-GitHub host', () => {
    // The keyless e2e lane lists repos on example.com; the derivation is about
    // the URL's last segment, not about GitHub.
    expect(projectNameFromRepoUrl('https://example.com/keyless/demo')).toBe('demo');
  });
});
