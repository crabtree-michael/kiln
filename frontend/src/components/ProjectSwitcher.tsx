// Project switcher (12 §4.1): a compact header control listing the user's
// projects + "New project…". The client references and keys each project by its
// `project_id` (DP5) — selecting one re-scopes every board/feed/stream/message
// call (the current-project store tears down and re-opens the EventSource
// against the new project). "New project…" routes to the dashboard's create
// form. Reads the live set + current selection from the current-project store;
// self-contained so it can drop into the header without threading props.
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

  // Nothing to switch between and no project resolved: render nothing (the gate
  // keeps the zero-project case off the app screen anyway).
  if (current === null) {
    return null;
  }

  return (
    <div data-role="project-switcher" ref={rootRef}>
      <button
        type="button"
        data-role="project-switcher-current"
        aria-haspopup="true"
        aria-expanded={open}
        aria-controls="project-switcher-panel"
        onClick={() => {
          setOpen((wasOpen) => !wasOpen);
        }}
      >
        {current.name}
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
        <button
          type="button"
          data-role="project-switcher-new"
          onClick={() => {
            setOpen(false);
            void navigate('/dashboard');
          }}
        >
          New project…
        </button>
      </div>
    </div>
  );
}
