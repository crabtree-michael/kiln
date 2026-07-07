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
import { Landing } from '@/landing/Landing';
import { Dashboard } from '@/dashboard/Dashboard';
import { AppErrorFallback } from '@/components/AppErrorFallback';
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

// `/` is the primary (08) screen; `/debug` keeps the original board+chat client
// (07) whole and unchanged as a developer view. `/landing` is the standalone
// marketing page — a stateless, scrolling page that reuses the design system and
// the real presentational components to show the product.
createRoot(root).render(
  <StrictMode>
    <Sentry.ErrorBoundary fallback={AppErrorFallback}>
      <BrowserRouter>
        <ThemeColorSync />
        <SentryRoutes>
          <Route path="/" element={<PrimaryScreen />} />
          <Route path="/landing" element={<Landing />} />
          <Route path="/dashboard/*" element={<Dashboard />} />
          <Route path="/debug" element={<App />} />
        </SentryRoutes>
      </BrowserRouter>
    </Sentry.ErrorBoundary>
  </StrictMode>,
);
