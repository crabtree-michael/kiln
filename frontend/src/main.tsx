// Design system: self-hosted variable fonts + the Kiln token sheet, loaded
// before any component module so component CSS can override base styles.
import '@fontsource-variable/hanken-grotesk';
import '@fontsource-variable/newsreader';
import '@fontsource-variable/spline-sans-mono';
import '@/styles/tokens.css';

import { StrictMode, useEffect } from 'react';
import { createRoot } from 'react-dom/client';
import {
  BrowserRouter,
  Route,
  Routes,
  createRoutesFromChildren,
  matchRoutes,
  useLocation,
  useNavigationType,
} from 'react-router-dom';
import * as Sentry from '@sentry/react';
import { PrimaryScreen } from '@/components/PrimaryScreen';
import { KanbanScreen } from '@/components/KanbanScreen';
import { DefaultRoute } from '@/components/DefaultRoute';
import { NotFound } from '@/components/NotFound';
import { Landing2 } from '@/landing/Landing2';
import { PrivateBeta } from '@/landing/PrivateBeta';
import { Guide } from '@/guide/Guide';
import { Dashboard } from '@/dashboard/Dashboard';
import { Signup } from '@/signup/Signup';
import { ProjectsManager } from '@/projects/ProjectsManager';
import { AppErrorFallback } from '@/components/AppErrorFallback';
import { SessionGate } from '@/components/SessionGate';
import { SessionProvider } from '@/stores/session';
import { CurrentProjectProvider } from '@/stores/current-project';
import { ThemeColorSync } from '@/components/ThemeColorSync';
import { installAssetRecovery } from '@/asset-recovery';
import { installPulsePhase } from '@/pulse-phase';

// Arm deploy-rollover recovery before anything else: if a hashed CSS/JS asset
// from a superseded deploy 404s (stale shell after a deploy, no SW to recover),
// force one full reload onto the current build's shell instead of rendering
// unstyled/blank. See asset-recovery.ts for the full rationale.
installAssetRecovery();

// Put every mark that shares a cadence on one clock, before anything renders, so
// the first mark to animate is already pinned. Marks that declare
// `--pulse-phase: shared` — the desk's in-progress head and the status marks
// listed under it — otherwise run the same tempo from whenever each happened to
// mount, and drift against each other permanently. See pulse-phase.ts.
installPulsePhase();

// Frontend error + trace reporting (spec-10 §3). The DSN is baked in at build
// time (`VITE_SENTRY_DSN`, a public value). When it is unset — local `pnpm dev`,
// plain `pnpm build`, and the vitest run — `enabled: false` turns every Sentry
// call into a no-op, so the app behaves exactly as it did before this wiring.
const dsn = import.meta.env.VITE_SENTRY_DSN;
const sentryEnabled = typeof dsn === 'string' && dsn.length > 0;

Sentry.init({
  dsn,
  enabled: sentryEnabled,
  environment: import.meta.env.MODE,
  // Tag events with the deploy's release when the build provides one.
  release: import.meta.env.VITE_RELEASE,
  integrations: [
    // React Router (this repo is on v7) tracing: parameterises navigation
    // transactions by route (`/`, `/app`) instead of raw URLs. Wired to the
    // component `<Routes>` pattern used below via the router hooks.
    Sentry.reactRouterBrowserTracingIntegration({
      useEffect,
      useLocation,
      useNavigationType,
      createRoutesFromChildren,
      matchRoutes,
    }),
  ],
  // One user (spec-10): sample every trace so nothing is dropped.
  tracesSampleRate: 1.0,
  // Session Replay is deliberately NOT added — keeping this first integration's
  // blast radius small. With no Replay integration, replay capture is off.
});

// Routing-instrumented <Routes> so the tracing integration above can name
// transactions after the matched route. Falls back to plain routing behaviour
// when Sentry is disabled.
const SentryRoutes = Sentry.wrapReactRouterRouting(Routes);

const root = document.getElementById('root');
if (root === null) {
  throw new Error('root element #root is missing from index.html');
}

// `/` is the site default (`DefaultRoute`): the marketing landing page for
// browser-tab visitors, but an installed web app (an iOS home-screen app, whose
// launch URL is pinned to `/` by the manifest `start_url`) is redirected
// straight to `/app` so it opens onto the board, not the marketing page. The
// landing page is a stateless, scrolling page reusing the design system and real
// presentational components. `/landing` and `/landing-2` stay pinned to it as
// aliases for everyone.
// `/app` is the primary (08) screen. It sits behind the session gate (11 phase
// 2): every `/api/*` call now requires a session cookie, so the gate resolves
// `GET /api/me` before the screen mounts its data providers (which immediately
// open SSE + fetch board/feed). `/dashboard`
// keeps its own existing gate, and `/signup` (the sign-up rehearsal) sits beside
// it. `/onboarding` is the onboarding guide
// (docs/onboarding.md) as a standalone, stateless styled page in the same
// design-system chrome. `/beta/pending` is the private-beta screen the GitHub
// callback redirects to when the allowlist turns a login away. The landing,
// onboarding, and private-beta pages stay public (no session gate) — the last of
// those especially, since everyone who reaches it was just refused a session.
createRoot(root).render(
  <StrictMode>
    <Sentry.ErrorBoundary fallback={AppErrorFallback}>
      <BrowserRouter>
        <ThemeColorSync />
        <SentryRoutes>
          <Route path="/" element={<DefaultRoute />} />
          <Route path="/landing" element={<Landing2 />} />
          <Route path="/landing-2" element={<Landing2 />} />
          <Route
            path="/app"
            element={
              <SessionProvider>
                <SessionGate>
                  <CurrentProjectProvider>
                    <PrimaryScreen />
                  </CurrentProjectProvider>
                </SessionGate>
              </SessionProvider>
            }
          />
          {/* `/kanban` is the desktop board view: the same shell and the same
              rail as `/app`, with the feed replaced by five columns of tickets
              (one per board state). Same session gate and same project scope —
              every `/api/*` call it makes is project-scoped, and it reads the
              same board store, so it is a second view of one truth rather than a
              second app. It is deliberately NOT a route the mobile shell
              switches on: a kanban board is a desk reading. */}
          <Route
            path="/kanban"
            element={
              <SessionProvider>
                <SessionGate>
                  <CurrentProjectProvider>
                    <KanbanScreen />
                  </CurrentProjectProvider>
                </SessionGate>
              </SessionProvider>
            }
          />
          <Route path="/onboarding" element={<Guide />} />
          <Route path="/beta/pending" element={<PrivateBeta />} />
          <Route path="/dashboard/*" element={<Dashboard />} />
          {/* `/signup` replays the sign-up experience on demand: the same
              `SignIn`/`Onboarding` components the dashboard mounts, over a
              simulated store, so an already-onboarded account can walk the whole
              flow again — either path — without being wiped first and without
              writing anything. Like `/dashboard`, it owns its own provider and
              so mounts outside the app's SessionGate. */}
          <Route path="/signup" element={<Signup />} />
          {/* `/projects` is the app-native project-management page (12 follow-up):
              list / create / configure / delete projects in the app's own chrome,
              where the header switcher's "Add" and the account view's projects
              detour used to route. It owns its own `DashboardProvider` (the shared
              data layer), so — like `/dashboard` — it mounts directly, not behind
              the app's SessionGate. */}
          <Route path="/projects" element={<ProjectsManager />} />
          {/* Catch-all last: every real route above wins first; any other path
              lands on the standalone 404 page instead of rendering nothing. */}
          <Route path="*" element={<NotFound />} />
        </SentryRoutes>
      </BrowserRouter>
    </Sentry.ErrorBoundary>
  </StrictMode>,
);
