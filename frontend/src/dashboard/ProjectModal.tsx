// The settings project modal (projects-in-a-modal): one project's whole
// configuration, opened from its panel in the Projects section.
//
// Why a modal at all, on a page whose stated rule is "the nav scrolls, it never
// swaps panes": that rule is about the page's own sections, which must all stay
// findable at once. A project's configuration is the opposite shape — a dozen
// fields belonging to ONE of N projects, where showing every project's form
// inline turned the section into a wall of near-identical forms with no way to
// scan the list. So the list stays flat and always visible, and the depth moves
// into a dialog the user opens deliberately.
//
// Three things the shell owns (the fields themselves are `ProjectFields` in its
// `detail` layout, so the modal never duplicates form state or the submit body):
//
//   * Dismissal — Escape, the scrim, and an explicit Close button. There is no
//     native <dialog> here: jsdom (the whole DOM suite) ships no
//     HTMLDialogElement, so a native dialog would be untestable in the gate.
//   * Focus — the panel takes focus on open and hands it back to whatever
//     opened it on close, so a keyboard user isn't dropped at the top of the
//     document.
//   * Closing on a SUCCESSFUL write only. `onSave`/`onDelete` resolve a boolean
//     from the store, so a failed save keeps the modal (and everything typed
//     into it) open above the page's error banner instead of discarding it.
import { useCallback, useEffect, useRef, type JSX, type MouseEvent, type ReactNode } from 'react';
import { ProjectFields } from '@/dashboard/ConfigFields';
import { useSandboxCatalog } from '@/dashboard/use-sandbox-catalog';
import { CloseIcon, FolderIcon, TrashIcon } from '@/dashboard/icons';
import type { GitHubRepos } from '@/dashboard/use-github-repos';
import type { MeProject, ProjectUpdateRequest, ProviderDescriptor } from '@/transport/transport';

/** What Tab can land on inside the dialog. Deliberately a plain list rather than
 * anything clever: the dialog only ever holds links, buttons, and form controls. */
const FOCUSABLE = 'a[href], button, input, select, textarea, [tabindex]:not([tabindex="-1"])';

/** Keeps Tab inside the dialog. `aria-modal` tells a screen reader the rest of
 * the page is inert, but it does nothing for a sighted keyboard user — without
 * this, Tab walks straight out of the dialog and into the settings page behind
 * the scrim. */
function trapTab(event: KeyboardEvent, panel: HTMLElement | null): void {
  if (panel === null) {
    return;
  }
  const focusable = [...panel.querySelectorAll<HTMLElement>(FOCUSABLE)].filter(
    (element) => !element.hasAttribute('disabled'),
  );
  const first = focusable.at(0);
  const last = focusable.at(-1);
  if (first === undefined || last === undefined) {
    return;
  }
  const active = document.activeElement;
  // Backwards off the first element (or off the panel itself, which holds focus
  // right after opening) wraps to the last, and forwards off the last wraps back.
  if (event.shiftKey && (active === first || active === panel)) {
    event.preventDefault();
    last.focus();
  } else if (!event.shiftKey && active === last) {
    event.preventDefault();
    first.focus();
  }
}

interface ProjectBodyProps {
  project: MeProject;
  providers: ProviderDescriptor[];
  github: GitHubRepos;
  /** The delete control, rendered at the leading edge of the form's action bar
   * (see `footerLead` on `ProjectFields`). */
  footerLead: ReactNode;
  saving: boolean;
  onSave: (body: ProjectUpdateRequest) => Promise<void>;
}

/** An existing project's fields, plus its own sandbox catalog. Split into its
 * own component because `useSandboxCatalog` is keyed by project id: there is no
 * id to load in create mode, and a hook can't be called conditionally. Mounting
 * it here (rather than per panel, as the old inline cards did) also means the
 * catalog is fetched for the one project actually being looked at. */
function ExistingProjectBody({
  project,
  providers,
  github,
  footerLead,
  saving,
  onSave,
}: ProjectBodyProps): JSX.Element {
  const catalog = useSandboxCatalog(project.id);
  return (
    <ProjectFields
      layout="detail"
      project={project}
      github={github}
      providers={providers}
      snapshots={catalog.snapshots}
      catalogAvailable={catalog.catalogAvailable}
      footerLead={footerLead}
      saving={saving}
      onSave={onSave}
    />
  );
}

export interface ProjectModalProps {
  /** The project being edited. Absent while creating one — the modal then runs
   * the same form against `POST /api/projects` and offers no delete. */
  project?: MeProject | undefined;
  providers: ProviderDescriptor[];
  github: GitHubRepos;
  saving: boolean;
  /** Commits the form. Resolves `true` only when the write landed; the modal
   * closes on `true` and stays open on `false`. */
  onSave: (body: ProjectUpdateRequest) => Promise<boolean>;
  /** Deletes this project (behind a confirm). Absent in create mode. */
  onDelete?: (() => Promise<boolean>) | undefined;
  onClose: () => void;
}

export function ProjectModal({
  project,
  providers,
  github,
  saving,
  onSave,
  onDelete,
  onClose,
}: ProjectModalProps): JSX.Element {
  const panelRef = useRef<HTMLDivElement>(null);

  // Dismissal, focus, and the page behind, for as long as the modal is mounted.
  // `onClose` is a stable store-backed callback at every call site, so this runs
  // once per open.
  useEffect(() => {
    const opener = document.activeElement;
    panelRef.current?.focus();
    // The scrim covers the viewport, so scrolling the page behind it is only
    // ever an accident.
    const pageOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';

    const handleKeyDown = (event: KeyboardEvent): void => {
      if (event.key === 'Escape') {
        onClose();
        return;
      }
      if (event.key === 'Tab') {
        trapTab(event, panelRef.current);
      }
    };
    document.addEventListener('keydown', handleKeyDown);

    return () => {
      document.removeEventListener('keydown', handleKeyDown);
      document.body.style.overflow = pageOverflow;
      // Hand focus back to whatever opened the modal — the project panel — but
      // only if it is still in the document (a deleted project's panel is not).
      if (opener instanceof HTMLElement && opener.isConnected) {
        opener.focus();
      }
    };
  }, [onClose]);

  const handleScrimMouseDown = useCallback(
    (event: MouseEvent<HTMLDivElement>): void => {
      // Only a press that starts on the scrim itself dismisses: a drag that
      // began inside the panel (selecting text, dragging a select open) must
      // not close it just because it ended out here.
      if (event.target === event.currentTarget) {
        onClose();
      }
    },
    [onClose],
  );

  const handleSave = useCallback(
    async (body: ProjectUpdateRequest): Promise<void> => {
      if (await onSave(body)) {
        onClose();
      }
    },
    [onSave, onClose],
  );

  const handleDelete = useCallback((): void => {
    if (onDelete === undefined || project === undefined) {
      return;
    }
    // A native confirm keeps the destructive gate simple; a project delete is a
    // real cross-module cascade and can't be undone (12 §5).
    if (!window.confirm(`Delete project “${project.name}”? This can't be undone.`)) {
      return;
    }
    void onDelete().then((deleted) => {
      if (deleted) {
        onClose();
      }
    });
  }, [onDelete, onClose, project]);

  // The trash glyph alone, at the leading edge of the form's action bar. Like
  // the modal's ×, it carries no visible label, so its accessible name has to
  // come from `aria-label` (and `title` gives a pointer user the same word on
  // hover). A paragraph of danger-zone copy said nothing the confirm dialog
  // doesn't say at the moment it matters.
  const deleteButton =
    onDelete !== undefined && project !== undefined ? (
      <button
        type="button"
        data-role="delete-project"
        data-variant="danger"
        aria-label="Delete project"
        title="Delete project"
        disabled={saving}
        onClick={handleDelete}
      >
        <TrashIcon />
      </button>
    ) : null;

  return (
    <div data-role="modal-scrim" onMouseDown={handleScrimMouseDown}>
      <div
        ref={panelRef}
        data-role="project-modal"
        role="dialog"
        aria-modal="true"
        // The visible title is an editable input (the name IS the header), so
        // there is no static heading to point `aria-labelledby` at.
        aria-label={project === undefined ? 'New project' : `Project settings: ${project.name}`}
        tabIndex={-1}
      >
        <header data-role="project-modal-bar">
          <span data-role="project-modal-eyebrow">
            <FolderIcon />
            {project === undefined ? 'New project' : 'Project settings'}
          </span>
          {/* The × alone. "Close" beside a close glyph is a word that tells a
              sighted reader nothing the glyph didn't; the name a screen reader
              needs comes from `aria-label`, and `title` gives a pointer user the
              same word on hover. */}
          <button
            type="button"
            data-role="close-project-modal"
            aria-label="Close"
            title="Close"
            onClick={onClose}
          >
            <CloseIcon />
          </button>
        </header>

        <div data-role="project-modal-body">
          {project === undefined ? (
            // Create mode has nothing to delete, so its action bar is the save
            // button alone — still at the trailing edge.
            <ProjectFields
              layout="detail"
              github={github}
              providers={providers}
              saving={saving}
              onSave={handleSave}
            />
          ) : (
            <ExistingProjectBody
              project={project}
              providers={providers}
              github={github}
              footerLead={deleteButton}
              saving={saving}
              onSave={handleSave}
            />
          )}
        </div>
      </div>
    </div>
  );
}
