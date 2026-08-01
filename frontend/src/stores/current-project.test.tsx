// Current-project store tests (12 §4.1, DP5): resolving the current project
// (deep-link `?project=` > localStorage MRU > first), scoping the transport
// layer to it, switching, and auto-switching to the project a tapped
// notification names (12 §6.3). Renders CurrentProjectProvider under a stub
// SessionContext (the source of `me.projects`).
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { useState, type JSX } from 'react';
import { CurrentProjectProvider } from '@/stores/current-project';
import { useCurrentProject } from '@/stores/current-project-context';
import { takeDeepLinkTicket } from '@/stores/deep-link';
import { useDeepLinkTicket } from '@/components/use-deep-link-ticket';
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

/** Stands in for the primary screen inside the keyed subtree: it holds the
 * open-ticket state a project switch tears down, so it proves the tapped ticket
 * survives the remount (12 §6.3). */
function TicketProbe(): JSX.Element {
  const [openTicketId, setOpenTicketId] = useState<string | null>(null);
  useDeepLinkTicket(setOpenTicketId);
  return <div data-testid="open-ticket">{openTicketId ?? 'none'}</div>;
}

function renderWith(projects: MeProject[]): void {
  const session: SessionStoreValue = { status: 'ready', me: makeMe(projects) };
  render(
    <SessionContext.Provider value={session}>
      <CurrentProjectProvider>
        <Probe />
        <TicketProbe />
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

  it('persists a ?project= deep-link as the MRU so the tap survives a reload', () => {
    window.localStorage.setItem('kiln.currentProjectId', 'p1');
    window.history.replaceState({}, '', '/app?project=p2');
    renderWith([makeProject('p1', 'one'), makeProject('p2', 'two')]);
    expect(window.localStorage.getItem('kiln.currentProjectId')).toBe('p2');
  });
});

/** A notification tapped while the tab is already open: the service worker
 * postMessages the deep link instead of reloading, so the store must do the
 * switch client-side (12 §6.3). */
describe('CurrentProjectProvider — a live notification tap', () => {
  let swTarget: EventTarget;
  let originalSw: PropertyDescriptor | undefined;

  beforeEach(() => {
    window.localStorage.clear();
    window.history.replaceState({}, '', '/app');
    swTarget = new EventTarget();
    originalSw = Object.getOwnPropertyDescriptor(navigator, 'serviceWorker');
    Object.defineProperty(navigator, 'serviceWorker', { configurable: true, value: swTarget });
  });

  afterEach(() => {
    if (originalSw) {
      Object.defineProperty(navigator, 'serviceWorker', originalSw);
    } else {
      Reflect.deleteProperty(navigator, 'serviceWorker');
    }
    takeDeepLinkTicket();
    window.localStorage.clear();
    window.history.replaceState({}, '', '/app');
  });

  function tap(url: string): void {
    act(() => {
      swTarget.dispatchEvent(new MessageEvent('message', { data: { type: 'kiln:navigate', url } }));
    });
  }

  it('switches to the notification’s project and opens its ticket there', () => {
    renderWith([makeProject('p1', 'one'), makeProject('p2', 'two')]);
    expect(screen.getByTestId('current').textContent).toBe('p1');

    tap('/app?project=p2&ticket=t-9');

    // The app moved to the firing project — transport re-scoped, MRU updated —
    // and the tapped ticket opened on the *new* project's screen, surviving the
    // remount the switch forces.
    expect(screen.getByTestId('current').textContent).toBe('p2');
    expect(getActiveProjectId()).toBe('p2');
    expect(window.localStorage.getItem('kiln.currentProjectId')).toBe('p2');
    expect(screen.getByTestId('open-ticket').textContent).toBe('t-9');
  });

  it('switches on a ticketless notification too', () => {
    renderWith([makeProject('p1', 'one'), makeProject('p2', 'two')]);
    tap('/app?project=p2');
    expect(screen.getByTestId('current').textContent).toBe('p2');
    expect(screen.getByTestId('open-ticket').textContent).toBe('none');
  });

  it('opens the ticket in place when the tap names the current project', () => {
    renderWith([makeProject('p1', 'one'), makeProject('p2', 'two')]);
    tap('/app?project=p1&ticket=t-3');
    expect(screen.getByTestId('current').textContent).toBe('p1');
    expect(screen.getByTestId('open-ticket').textContent).toBe('t-3');
    // Nothing was parked for a remount that never happens — a later switch must
    // not resurrect this ticket.
    expect(takeDeepLinkTicket()).toBeNull();
  });

  it('stays put when the tap names a project the user does not have', () => {
    // Deleted since the notification fired, or another user's: selecting an
    // unresolvable id would silently land on the first project instead.
    window.localStorage.setItem('kiln.currentProjectId', 'p2');
    renderWith([makeProject('p1', 'one'), makeProject('p2', 'two')]);
    tap('/app?project=gone&ticket=t-4');
    expect(screen.getByTestId('current').textContent).toBe('p2');
    expect(getActiveProjectId()).toBe('p2');
    expect(takeDeepLinkTicket()).toBeNull();
  });
});
