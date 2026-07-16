// Current-project store tests (12 §4.1, DP5): resolving the current project
// (deep-link `?project=` > localStorage MRU > first), scoping the transport
// layer to it, and switching. Renders CurrentProjectProvider under a stub
// SessionContext (the source of `me.projects`).
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import type { JSX } from 'react';
import { CurrentProjectProvider } from '@/stores/current-project';
import { useCurrentProject } from '@/stores/current-project-context';
import { SessionContext, type SessionStoreValue } from '@/stores/session-context';
import { getActiveProjectId } from '@/transport/transport';
import type { Me, MeProject } from '@/transport/transport';

function makeProject(id: string, name: string): MeProject {
  return {
    id,
    name,
    repo_url: `https://github.com/x/${name}`,
    agent_provider: '',
    amika_snapshot: '',
    worker_count: 3,
    merge_gate_mode: 'main',
    amika_secrets: [],
  };
}

function makeMe(projects: MeProject[]): Me {
  return {
    user: { github_login: 'octocat', display_name: 'Octocat', avatar_url: '' },
    projects,
    settings: {
      anthropic_api_key: { set: false, tail: '' },
      amika_api_key: { set: false, tail: '' },
      devin_api_key: { set: false, tail: '' },
      github_auth_token: { set: false, tail: '' },
      amika_claude_cred_id: '',
    },
  };
}

/** Probe exposing the current id and a switch control. */
function Probe(): JSX.Element {
  const { current, projects, selectProject } = useCurrentProject();
  return (
    <div>
      <div data-testid="current">{current?.id ?? 'none'}</div>
      {projects.map((p) => (
        <button
          key={p.id}
          type="button"
          data-testid={`switch-${p.id}`}
          onClick={() => {
            selectProject(p.id);
          }}
        >
          {p.name}
        </button>
      ))}
    </div>
  );
}

function renderWith(projects: MeProject[]): void {
  const session: SessionStoreValue = { status: 'ready', me: makeMe(projects) };
  render(
    <SessionContext.Provider value={session}>
      <CurrentProjectProvider>
        <Probe />
      </CurrentProjectProvider>
    </SessionContext.Provider>,
  );
}

describe('CurrentProjectProvider', () => {
  beforeEach(() => {
    window.localStorage.clear();
    window.history.replaceState({}, '', '/app');
  });

  afterEach(() => {
    window.localStorage.clear();
    window.history.replaceState({}, '', '/app');
  });

  it('defaults to the first project and scopes transport to it', () => {
    renderWith([makeProject('p1', 'one'), makeProject('p2', 'two')]);
    expect(screen.getByTestId('current').textContent).toBe('p1');
    expect(getActiveProjectId()).toBe('p1');
  });

  it('switching re-scopes transport and persists the MRU', () => {
    renderWith([makeProject('p1', 'one'), makeProject('p2', 'two')]);
    fireEvent.click(screen.getByTestId('switch-p2'));
    expect(screen.getByTestId('current').textContent).toBe('p2');
    expect(getActiveProjectId()).toBe('p2');
    expect(window.localStorage.getItem('kiln.currentProjectId')).toBe('p2');
  });

  it('honours the localStorage MRU on load', () => {
    window.localStorage.setItem('kiln.currentProjectId', 'p2');
    renderWith([makeProject('p1', 'one'), makeProject('p2', 'two')]);
    expect(screen.getByTestId('current').textContent).toBe('p2');
    expect(getActiveProjectId()).toBe('p2');
  });

  it('honours a ?project= deep-link over the MRU (a notification tap, 12 §6.3)', () => {
    window.localStorage.setItem('kiln.currentProjectId', 'p1');
    window.history.replaceState({}, '', '/app?project=p2');
    renderWith([makeProject('p1', 'one'), makeProject('p2', 'two')]);
    expect(screen.getByTestId('current').textContent).toBe('p2');
  });

  it('falls back to the first project when the selected id no longer exists', () => {
    window.localStorage.setItem('kiln.currentProjectId', 'deleted-id');
    renderWith([makeProject('p1', 'one'), makeProject('p2', 'two')]);
    expect(screen.getByTestId('current').textContent).toBe('p1');
  });
});
