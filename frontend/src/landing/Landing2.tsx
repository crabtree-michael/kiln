// Marketing landing page — the default page every visitor lands on at `/`, also
// served at the `/landing` and `/landing-2` aliases. A
// standalone, scrolling page that positions Kiln as a coding agent orchestrator
// you can run from anywhere you are — phone, desk, or on the move, by voice or
// by tap. It is NOT part of the app shell: it holds no state, opens no stream/mic, and
// mounts no provider. It only reuses the Kiln design tokens
// (src/styles/tokens.css) for its chrome, so light/dark comes for free and the
// page follows the OS theme via resolveTheme (src/theme.ts), exactly like `/`.
//
// The product shots are the same real captures of the running app
// (frontend/public/shots/*.png) that `/landing` frames; the ones with a light
// and a dark capture are served through <picture> with a prefers-color-scheme
// source so each screenshot matches the page theme. The board is captured from
// `/debug`, which the app renders dark-only, so it ships a single dark shot.
import { useState, type JSX } from 'react';
import { Link } from 'react-router-dom';
import { BetaSignupForm } from '@/landing/BetaSignupForm';
import { BetaModal } from '@/landing/BetaModal';
import '@/landing/Landing2.css';

/** The Kiln bell mark (public/kiln-mark.svg), inlined so it can take the accent
 * token for its fill in the nav / footer / CTA lockups. */
function KilnGlyph({ size = 26 }: { size?: number }): JSX.Element {
  return (
    <svg width={size} height={size} viewBox="0 0 96 96" aria-hidden="true" focusable="false">
      <path
        d="M48 12 C33 12 25 25 25 43 C25 56 21 63 16.5 67.5 C14.5 69.7 16 74 19.5 74 H76.5 C80 74 81.5 69.7 79.5 67.5 C75 63 71 56 71 43 C71 25 63 12 48 12 Z"
        fill="var(--accent)"
      />
      <path d="M39 78 A9 9 0 0 0 57 78 Z" fill="var(--accent)" />
    </svg>
  );
}

/** The GitHub mark (invertocat), a single path so it inherits currentColor and
 * follows the page theme. Used by the "Connect to GitHub" step. */
function GitHubMark(): JSX.Element {
  return (
    <svg width="30" height="30" viewBox="0 0 16 16" aria-hidden="true" focusable="false">
      <path
        fill="currentColor"
        fillRule="evenodd"
        d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82a7.65 7.65 0 0 1 2-.27c.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.01 8.01 0 0 0 16 8c0-4.42-3.58-8-8-8z"
      />
    </svg>
  );
}

/** A bell glyph for the "use it anywhere" step — stroked with currentColor,
 * matching the weight of the other step marks so it reads in both themes. */
function BellMark(): JSX.Element {
  return (
    <svg
      width="28"
      height="28"
      viewBox="0 0 24 24"
      aria-hidden="true"
      focusable="false"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
      strokeLinejoin="round"
    >
      <path d="M18 8.5a6 6 0 0 0-12 0c0 6.5-2.5 8.5-2.5 8.5h17s-2.5-2-2.5-8.5" />
      <path d="M10.3 20.5a2 2 0 0 0 3.4 0" />
    </svg>
  );
}

/** A theme-swapped screenshot: the dark capture in dark mode, the light capture
 * otherwise. `base` is the shared filename stem under /shots (e.g. "feed"). */
function ThemedShot({
  base,
  alt,
  className,
}: {
  base: string;
  alt: string;
  className: string;
}): JSX.Element {
  return (
    <picture className={className}>
      <source srcSet={`/shots/${base}-dark.png`} media="(prefers-color-scheme: dark)" />
      <img src={`/shots/${base}-light.png`} alt={alt} loading="lazy" decoding="async" />
    </picture>
  );
}

/** The primary screen (activity feed) inside a phone bezel — a real capture of
 * `/`: blocker pinned on top, proposals to accept, an image preview, the dock. */
function PhoneFeedShot(): JSX.Element {
  return (
    <ThemedShot
      base="feed"
      className="shot-phone"
      alt="The Kiln activity feed on a phone: a blocker pinned to the top, two proposals with Accept buttons, an image preview, and the voice dock at the bottom."
    />
  );
}

/** The board, captured from `/debug` (dark-only), framed as a screen. */
function BoardShot(): JSX.Element {
  return (
    <figure className="shot-window">
      <div className="shot-window__bar" aria-hidden="true">
        <span />
        <span />
        <span />
      </div>
      <img
        src="/shots/board-dark.png"
        alt="The Kiln board: three columns — Backlog (Shaping, Ready), Developing (a fire-red Blocked card above Working), and Done."
        loading="lazy"
        decoding="async"
      />
    </figure>
  );
}

/** The voice dock, close up — the mic control captured from the running app. */
function DockShot(): JSX.Element {
  return (
    <ThemedShot
      base="dock"
      className="shot-dock"
      alt="The Kiln voice dock: a microphone button labelled Tap to talk, with a keyboard toggle."
    />
  );
}

/** The places Kiln comes with you — the heart of the "anywhere you are" pitch.
 * Each is a moment away from a desk where you can still steer the work. */
const ANYWHERE: { icon: string; title: string; body: string }[] = [
  {
    icon: '📱',
    title: 'On your phone',
    body: 'The whole board and feed live in a mobile-first web client. No install, no laptop — open a tab and your team of agents is right there.',
  },
  {
    icon: '🎧',
    title: 'Hands-free, by voice',
    body: 'Walking, driving, or away from a keyboard? Speak the work. The orchestrator hears you out, shapes the ticket, and answers back out loud.',
  },
  {
    icon: '🛋️',
    title: 'Off the clock',
    body: 'Agents keep building in the cloud while you step away. Progress waits quietly in the feed for whenever you next look — nothing stalls.',
  },
  {
    icon: '🔔',
    title: 'The moment it matters',
    body: "When an agent hits a real decision, Kiln pushes you a notification wherever you are. Tap in, answer, and it's moving again.",
  },
];

/** The coding agents Kiln can drive, shown in the "cloud agents" step as three
 * circular brand marks orbiting a centre point. `logo` is the mark under /logos,
 * sat on a white disc so each dark logo reads on either theme. */
const AGENTS: { name: string; logo: string }[] = [
  { name: 'Cursor', logo: '/logos/cursor.svg' },
  { name: 'Devin', logo: '/logos/devin.svg' },
  { name: 'Amika', logo: '/logos/amika.svg' },
];

const FEATURES: {
  eyebrow: string;
  title: string;
  body: string;
  shot: JSX.Element;
  flip?: boolean;
}[] = [
  {
    eyebrow: 'The board',
    title: 'Your whole operation, in your pocket.',
    body: 'Every ticket moves through Backlog, Developing, and Done on one live board that fits a phone screen. Work-in-progress is hard-capped at the number of free workers, so the queue can never run away from you — no matter where you are watching it from.',
    shot: <BoardShot />,
  },
  {
    eyebrow: 'The activity feed',
    title: 'Catch up in ten seconds, anywhere.',
    body: 'Your home screen is a backlog of notifications ordered by urgency: blockers pinned to the top, proposals next, then updates newest-first. A divider marks what is new since your last visit, so a glance on the go tells you exactly what changed.',
    shot: <PhoneFeedShot />,
    flip: true,
  },
];

export function Landing2(): JSX.Element {
  const [betaOpen, setBetaOpen] = useState(false);
  const openBeta = (): void => {
    setBetaOpen(true);
  };
  return (
    <div className="kiln-landing-2">
      <header className="kiln-nav">
        <div className="kiln-nav__inner">
          <Link to="/" className="kiln-nav__brand" aria-label="Kiln home">
            <KilnGlyph size={28} />
            <span className="kiln-nav__wordmark">Kiln</span>
          </Link>
          <nav className="kiln-nav__links" aria-label="Primary">
            <a href="#anywhere">Anywhere</a>
            <a href="#how">How it works</a>
            <a href="#surfaces">The surfaces</a>
          </nav>
          <div className="kiln-nav__actions">
            {/* Sign-in is a plain full-page anchor — NOT a router Link — because
                `/auth/github/login` is a backend route the SPA does not own
                (mirrors SessionGate / dashboard SignIn). It sits beside the beta
                CTA so returning users have a way straight into the app. */}
            <a href="/auth/github/login" className="kiln-btn kiln-btn--ghost kiln-nav__signin">
              Sign in
            </a>
            <button
              type="button"
              className="kiln-btn kiln-btn--primary kiln-nav__cta"
              onClick={openBeta}
            >
              Join the beta
            </button>
          </div>
        </div>
      </header>

      <main>
        <section className="kiln-hero">
          <div className="kiln-hero__copy">
            <span className="kiln-eyebrow">Coding agent orchestrator</span>
            <h1 className="kiln-hero__title">
              Orchestrate a team of coding agents{' '}
              <span className="kiln-accent">from anywhere you are.</span>
            </h1>
            <p className="kiln-hero__lead">
              Kiln runs your autonomous coding agents in the cloud and puts the whole operation in
              your pocket. Steer the work from your phone, your desk, or your voice — on the train,
              between meetings, or from the couch. Your team never has to wait for you to sit down.
            </p>
            <div className="kiln-hero__actions">
              <BetaSignupForm cta="Join the beta" />
              <a href="#how" className="kiln-btn kiln-btn--ghost kiln-btn--lg kiln-hero__secondary">
                How it works
              </a>
            </div>
            <ul className="kiln-hero__chips" aria-label="Steer Kiln from">
              <li>On your phone</li>
              <li>At your desk</li>
              <li>By voice</li>
              <li>While you&rsquo;re away</li>
            </ul>
          </div>
          <div className="kiln-hero__art">
            <PhoneFeedShot />
          </div>
        </section>

        <section id="anywhere" className="kiln-section kiln-anywhere">
          <div className="kiln-section__head">
            <span className="kiln-eyebrow">Anywhere you are</span>
            <h2 className="kiln-section__title">Your team of agents comes with you.</h2>
            <p className="kiln-section__lead">
              Kiln lives in the cloud, so there is no environment to boot and no laptop to be
              chained to. Wherever you happen to be, the work is one tab or one sentence away.
            </p>
          </div>
          <ul className="kiln-anywhere__grid">
            {ANYWHERE.map((place) => (
              <li className="kiln-anywhere__card" key={place.title}>
                <span className="kiln-anywhere__icon" aria-hidden="true">
                  {place.icon}
                </span>
                <h3>{place.title}</h3>
                <p>{place.body}</p>
              </li>
            ))}
          </ul>
        </section>

        <section id="how" className="kiln-section kiln-how">
          <div className="kiln-section__head">
            <span className="kiln-eyebrow">How it works</span>
            <h2 className="kiln-section__title">Up and running in three steps.</h2>
            <p className="kiln-section__lead">
              Connect your repo, bring your agents, and steer from anywhere. Kiln handles the
              orchestration in between.
            </p>
          </div>
          <ol className="kiln-how__grid">
            <li className="kiln-how__step">
              <span className="kiln-how__num">01</span>
              <div className="kiln-how__marks">
                <GitHubMark />
              </div>
              <h3>Connect to GitHub</h3>
              <p>
                Point Kiln at your repository. It signs in through GitHub and ships every change as
                a pull request you review.
              </p>
            </li>
            <li className="kiln-how__step">
              <span className="kiln-how__num">02</span>
              <div className="kiln-how__orbit">
                <span className="kiln-how__orbit-ring" aria-hidden="true" />
                {AGENTS.map((agent) => (
                  <span className="kiln-how__orbit-node" key={agent.name}>
                    <span className="kiln-how__orbit-badge">
                      <img src={agent.logo} alt={agent.name} loading="lazy" decoding="async" />
                    </span>
                  </span>
                ))}
              </div>
              <h3>Set up with cloud agents</h3>
              <p>
                Bring your coding agents — Cursor, Devin, or Amika. Each runs in its own cloud
                sandbox and pulls tickets on its own, no machine of yours involved.
              </p>
            </li>
            <li className="kiln-how__step">
              <span className="kiln-how__num">03</span>
              <div className="kiln-how__marks">
                <BellMark />
              </div>
              <h3>Use it anywhere</h3>
              <p>
                Steer the whole operation from your phone or your voice. Say the work, check in, or
                clear a blocker — from wherever you are.
              </p>
            </li>
          </ol>
        </section>

        <section id="surfaces" className="kiln-section kiln-features">
          {FEATURES.map((feature) => (
            <div
              className={`kiln-feature${feature.flip ? ' kiln-feature--flip' : ''}`}
              key={feature.title}
            >
              <div className="kiln-feature__copy">
                <span className="kiln-eyebrow">{feature.eyebrow}</span>
                <h2 className="kiln-feature__title">{feature.title}</h2>
                <p>{feature.body}</p>
              </div>
              <div className="kiln-feature__art">{feature.shot}</div>
            </div>
          ))}
        </section>

        <section className="kiln-section kiln-voice">
          <div className="kiln-voice__inner">
            <div className="kiln-voice__copy">
              <span className="kiln-eyebrow">Voice</span>
              <h2 className="kiln-section__title">No keyboard? Just talk.</h2>
              <p className="kiln-section__lead">
                When your hands are full or a desk is nowhere near, speak a message and the
                orchestrator reasons about it, then answers back — out loud. Ask for status,
                redirect an agent mid-flight, create or reprioritise tickets, or clear a blocker,
                all without typing. Kiln confirms anything destructive or ambiguous before it acts,
                so a mis-hear never quietly wrecks your work.
              </p>
              <div className="kiln-voice__actions">
                <button
                  type="button"
                  className="kiln-btn kiln-btn--primary kiln-btn--lg"
                  onClick={openBeta}
                >
                  Join the beta
                </button>
              </div>
            </div>
            <div className="kiln-voice__art">
              <DockShot />
            </div>
          </div>
        </section>
      </main>

      <footer className="kiln-footer">
        <div className="kiln-footer__inner">
          <div className="kiln-footer__brand">
            <KilnGlyph size={22} />
            <span>Kiln</span>
          </div>
          <nav className="kiln-footer__links" aria-label="Footer">
            <button type="button" className="kiln-footer__link-btn" onClick={openBeta}>
              Join the beta
            </button>
            <a href="#anywhere">Anywhere</a>
            <a href="#how">How it works</a>
          </nav>
          <span className="kiln-footer__note">
            A coding agent orchestrator for anywhere you are.
          </span>
        </div>
      </footer>

      <BetaModal
        open={betaOpen}
        onClose={() => {
          setBetaOpen(false);
        }}
        heading="Take your agents anywhere."
        blurb="Kiln is in private beta. Leave your email and we’ll let you know the moment your spot is ready."
      />
    </div>
  );
}
