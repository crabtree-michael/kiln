// The desktop shell's left region, as one component (13 §3, §5, §10).
//
// It was inlined in `DesktopScreenView` until `/kanban` needed the *same* rail —
// not a rail that looks like it. That distinction is the whole reason this file
// exists: the brief for the board view is "the sidebar is identical to the
// desktop app's", and the only way to keep that true a year from now is for
// there to be exactly one of it. A copied `<aside>` would agree on the day it
// was written and drift on the first change to either screen.
//
// It carries the same four things it always did, in the same order — the
// wordmark, the projects list, the disconnected notice, and the actions row
// (notification bell + dashboard link) — and its markup is unchanged from when
// it lived in `DesktopScreenView`, so the existing DOM tests, the CSS in
// `DesktopScreen.css`, and the smoke script all still find what they look for.
//
// Presentational, like everything else in this directory: rows and callbacks in,
// markup out. The polling behind each row's state lives in `use-projects-status`.
import type { JSX } from 'react';
import type { NotificationModeValue } from '@/transport/transport';
import type { WebPushStatus } from '@/stores/use-web-push';
import { NotificationSettingsMenu } from '@/components/NotificationSettingsMenu';
import { ProjectsRail, type RailProject } from '@/components/desktop/ProjectsRail';

export interface DesktopRailProps {
  /** The rail's rows, in a stable order with each one's ambient state (13 §5). */
  projects: RailProject[];
  currentProjectId: string | null;
  onSelectProject: (id: string) => void;
  /** Whether the stream has dropped — surfaced at the rail's foot rather than
   * over the content, see the render below. */
  disconnected: boolean;
  notificationMode?: NotificationModeValue;
  onSelectNotificationMode?: ((mode: NotificationModeValue) => void) | undefined;
  pushStatus?: WebPushStatus | undefined;
  onEnablePush?: (() => void) | undefined;
  onDisablePush?: (() => void) | undefined;
}

export function DesktopRail({
  projects,
  currentProjectId,
  onSelectProject,
  disconnected,
  notificationMode = 'blocked',
  onSelectNotificationMode,
  pushStatus,
  onEnablePush,
  onDisablePush,
}: DesktopRailProps): JSX.Element {
  return (
    <aside data-role="desktop-rail">
      <div data-role="rail-head">
        <img data-role="kiln-glyph" src="/kiln-mark.svg" alt="" aria-hidden="true" />
        <span data-role="rail-wordmark">Kiln</span>
      </div>
      <ProjectsRail
        projects={projects}
        currentProjectId={currentProjectId}
        onSelectProject={onSelectProject}
      />
      <div data-role="rail-foot">
        {/* Disconnected lives at the foot of the rail: permanently visible,
            out of the feed's reading column, and next to the ambient layer it
            qualifies — every state above it is now a statement about the last
            thing we heard, not about now. */}
        {disconnected && (
          <div data-role="desktop-connection" role="status">
            <span data-role="desktop-connection-dot" aria-hidden="true" />
            Reconnecting — not receiving updates
          </div>
        )}
        <div data-role="rail-actions">
          <NotificationSettingsMenu
            mode={notificationMode}
            onSelectMode={onSelectNotificationMode}
            pushStatus={pushStatus}
            onEnablePush={onEnablePush}
            onDisablePush={onDisablePush}
          />
          {/* A plain anchor, not a router Link: `/dashboard` mounts its own
              provider tree and this shell is deliberately router-free (same
              stance as the mobile header's gear). */}
          <a data-role="rail-dashboard" href="/dashboard" aria-label="Dashboard">
            <svg data-role="header-gear" viewBox="0 0 20 20" aria-hidden="true">
              <path
                fill="none"
                stroke="currentColor"
                strokeWidth="1.5"
                strokeLinecap="round"
                strokeLinejoin="round"
                d="M9.00 2.87A7.2 7.2 0 0 1 11.00 2.87L11.31 4.76A5.4 5.4 0 0 1 12.78 5.37L14.33 4.25A7.2 7.2 0 0 1 15.75 5.67L14.63 7.22A5.4 5.4 0 0 1 15.24 8.69L17.13 9.00A7.2 7.2 0 0 1 17.13 11.00L15.24 11.31A5.4 5.4 0 0 1 14.63 12.78L15.75 14.33A7.2 7.2 0 0 1 14.33 15.75L12.78 14.63A5.4 5.4 0 0 1 11.31 15.24L11.00 17.13A7.2 7.2 0 0 1 9.00 17.13L8.69 15.24A5.4 5.4 0 0 1 7.22 14.63L5.67 15.75A7.2 7.2 0 0 1 4.25 14.33L5.37 12.78A5.4 5.4 0 0 1 4.76 11.31L2.87 11.00A7.2 7.2 0 0 1 2.87 9.00L4.76 8.69A5.4 5.4 0 0 1 5.37 7.22L4.25 5.67A7.2 7.2 0 0 1 5.67 4.25L7.22 5.37A5.4 5.4 0 0 1 8.69 4.76L9.00 2.87Z"
              />
              <circle cx="10" cy="10" r="2.4" fill="none" stroke="currentColor" strokeWidth="1.5" />
            </svg>
          </a>
        </div>
      </div>
    </aside>
  );
}
