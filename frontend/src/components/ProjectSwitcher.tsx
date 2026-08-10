// Project switcher (12 §4.1): the app header's brand control. The wordmark shows
// the current project's name in Kiln's brand styling (the big red accent
// wordmark; the bell glyph that used to precede it is gone from the nav bar) and
// is itself the dropdown trigger — clicking it opens a menu
// listing the user's projects (the current one marked) and, below a rule,
// "Settings", which opens the `/dashboard` account view. Settings is here
// because the header's standalone gear is gone: one dropdown now holds what the
// top bar used to spend an icon each on. Creating a project is NOT one of the
// phone's jobs here — the "Add" button that routed to `/projects?new=1` is gone,
// leaving this menu to do the one thing it is for: switch between the projects
// that exist. The project-management page still creates them.
// The client references and keys each project by
// its `project_id` (DP5); selecting one re-scopes every board/feed/stream/message
// call (the current-project store tears down and re-opens the EventSource against
// the new project). Reads the live set + current selection from the
// current-project store; self-contained so it drops into the header's brand slot
// (`PrimaryScreenView`) without threading props. It lives INSIDE the header — not
// as a floating sibling above the screen — so the header actions and the dock
// below it stay reachable.
import { useEffect, useRef, useState, type JSX } from 'react';
import { useNavigate } from 'react-router-dom';
import { useCurrentProject } from '@/stores/current-project-context';

export function ProjectSwitcher(): JSX.Element | null {
  const { current, projects, selectProject } = useCurrentProject();
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);
  const navigate = useNavigate();

  // While open, a click anywhere outside — or Escape — dismisses it (mirrors
  // HeaderStatusMenu's dismissal).
  useEffect(() => {
    if (!open) {
      return;
    }
    function onPointerDown(event: MouseEvent): void {
      const target = event.target;
      if (target instanceof Node && rootRef.current !== null && !rootRef.current.contains(target)) {
        setOpen(false);
      }
    }
    function onKeyDown(event: KeyboardEvent): void {
      if (event.key === 'Escape') {
        setOpen(false);
      }
    }
    document.addEventListener('mousedown', onPointerDown);
    document.addEventListener('keydown', onKeyDown);
    return () => {
      document.removeEventListener('mousedown', onPointerDown);
      document.removeEventListener('keydown', onKeyDown);
    };
  }, [open]);

  // No project resolved (the gate keeps the zero-project case off the app screen
  // anyway): render nothing so the header's brand slot falls back to the static
  // "Kiln" mark.
  if (current === null) {
    return null;
  }

  return (
    <div data-role="project-switcher" ref={rootRef}>
      <button
        type="button"
        data-role="project-switcher-current"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls="project-switcher-panel"
        onClick={() => {
          setOpen((wasOpen) => !wasOpen);
        }}
      >
        <span data-role="kiln-mark">
          <span data-role="kiln-wordmark">{current.name}</span>
        </span>
        <span data-role="project-switcher-caret" aria-hidden="true" />
      </button>
      <div
        id="project-switcher-panel"
        data-role="project-switcher-panel"
        data-open={open}
        aria-hidden={!open}
      >
        <ul data-role="project-switcher-list">
          {projects.map((project) => (
            <li key={project.id}>
              <button
                type="button"
                data-role="project-switcher-item"
                data-project-id={project.id}
                data-current={project.id === current.id ? 'true' : undefined}
                onClick={() => {
                  selectProject(project.id);
                  setOpen(false);
                }}
              >
                {project.name}
              </button>
            </li>
          ))}
        </ul>
        {/* Settings is the panel's last item, below a rule, because it is the one
            entry here that is not about a project: it opens the account view
            (`/dashboard`) the header's standalone gear used to. That gear is
            gone, so this is the way in — hence a router navigation rather than
            the gear's full-page anchor, which this component can do because it
            is already router-dependent. */}
        <div data-role="project-switcher-divider" aria-hidden="true" />
        <button
          type="button"
          data-role="project-switcher-settings"
          onClick={() => {
            setOpen(false);
            void navigate('/dashboard');
          }}
        >
          Settings
        </button>
      </div>
    </div>
  );
}
