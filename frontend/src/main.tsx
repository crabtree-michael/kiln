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
import { App } from '@/App';
import { PrimaryScreen } from '@/components/PrimaryScreen';
import { DefaultRoute } from '@/components/DefaultRoute';
import { Landing2 } from '@/landing/Landing2';
import { BetaThanks } from '@/landing/BetaThanks';
import { Guide } from '@/guide/Guide';
import { Dashboard } from '@/dashboard/Dashboard';
import { AppErrorFallback } from '@/components/AppErrorFallback';
import { SessionGate } from '@/components/SessionGate';
import { SessionProvider } from '@/stores/session';
import { ThemeColorSync } from '@/components/ThemeColorSync';
import { installAssetRecovery } from '@/asset-recovery';

// Arm deploy-rollover recovery before anything else: if a hashed CSS/JS asset
// from a superseded deploy 404s (stale shell after a deploy, no SW to recover),
// force one full reload onto the current build's shell instead of rendering
// unstyled/blank. See asset-recovery.ts for the full rationale.
installAssetRecovery();

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
    // transactions by route (`/`, `/debug`) instead of raw URLs. Wired to the
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
// `/app` is the primary (08) screen; `/debug` keeps the original board+chat
// client (07) whole and unchanged as a developer view. Both sit behind the
// session gate (11 phase 2): every `/api/*` call now requires a session cookie,
// so the gate resolves `GET /api/me` before either screen mounts its data
// providers (which immediately open SSE + fetch board/feed). `/dashboard`
// keeps its own existing gate. `/onboarding` is the onboarding guide
// (docs/onboarding.md) as a standalone, stateless styled page in the same
// design-system chrome. `/beta/thanks` is the confirmation page the beta-signup
// form redirects to. The landing, onboarding, and thanks pages stay public (no
// session gate).
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
                  <PrimaryScreen />
                </SessionGate>
              </SessionProvider>
            }
          />
          <Route path="/onboarding" element={<Guide />} />
          <Route path="/beta/thanks" element={<BetaThanks />} />
          <Route path="/dashboard/*" element={<Dashboard />} />
          <Route
            path="/debug"
            element={
              <SessionProvider>
                <SessionGate>
                  <App />
                </SessionGate>
              </SessionProvider>
            }
          />
        </SentryRoutes>
      </BrowserRouter>
    </Sentry.ErrorBoundary>
  </StrictMode>,
);
