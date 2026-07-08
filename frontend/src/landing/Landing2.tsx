// Marketing landing page (`/landing`, also served at the `/landing-2` alias). A
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
import type { JSX } from 'react';
import { Link } from 'react-router-dom';
import { BetaSignupForm } from '@/landing/BetaSignupForm';
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

const LOOP_STEPS: { n: string; title: string; body: string }[] = [
  {
    n: '01',
    title: 'Say the work, from anywhere',
    body: 'Open a tab or just talk. Describe what you want like you would to a lead; Kiln turns it into a ticket and shapes the details with you.',
  },
  {
    n: '02',
    title: 'Agents build in the cloud',
    body: 'Once a ticket is ready, a coding agent pulls it into its own sandbox and starts writing, running, and committing code — no machine of yours involved.',
  },
  {
    n: '03',
    title: 'It stays quiet',
    body: 'Routine progress lands in the activity feed for whenever you look. No pings, no noise — you carry on with your day.',
  },
  {
    n: '04',
    title: 'It finds you when needed',
    body: 'When a decision comes up, the ticket goes Blocked and Kiln notifies you wherever you are. Answer by voice or tap, and it resumes.',
  },
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
  return (
    <div className="kiln-landing-2">
      <header className="kiln-nav">
        <div className="kiln-nav__inner">
          <Link to="/landing" className="kiln-nav__brand" aria-label="Kiln home">
            <KilnGlyph size={28} />
            <span className="kiln-nav__wordmark">Kiln</span>
          </Link>
          <nav className="kiln-nav__links" aria-label="Primary">
            <a href="#anywhere">Anywhere</a>
            <a href="#how">How it works</a>
            <a href="#surfaces">The surfaces</a>
          </nav>
          <a href="#beta" className="kiln-btn kiln-btn--primary kiln-nav__cta">
            Join the beta
          </a>
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
                See it anywhere
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

        <section id="how" className="kiln-section kiln-loop">
          <div className="kiln-section__head">
            <span className="kiln-eyebrow">The loop</span>
            <h2 className="kiln-section__title">Hands-off until you are genuinely needed.</h2>
            <p className="kiln-section__lead">
              Kiln is built to minimise interruption and free you from your desk. Agents work
              silently; you step in — from wherever you are — only for the decisions that move the
              work forward.
            </p>
          </div>
          <ol className="kiln-loop__grid">
            {LOOP_STEPS.map((step) => (
              <li className="kiln-loop__step" key={step.n}>
                <span className="kiln-loop__num">{step.n}</span>
                <h3>{step.title}</h3>
                <p>{step.body}</p>
              </li>
            ))}
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
                <a href="#beta" className="kiln-btn kiln-btn--primary kiln-btn--lg">
                  Join the beta
                </a>
              </div>
            </div>
            <div className="kiln-voice__art">
              <DockShot />
            </div>
          </div>
        </section>

        <section id="beta" className="kiln-cta">
          <div className="kiln-cta__inner">
            <KilnGlyph size={44} />
            <h2>Take your agents anywhere.</h2>
            <p>
              Kiln is in private beta. Leave your email and we&rsquo;ll let you know the moment your
              spot is ready.
            </p>
            <BetaSignupForm cta="Notify me" />
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
            <a href="#beta">Join the beta</a>
            <a href="#anywhere">Anywhere</a>
            <a href="#how">How it works</a>
          </nav>
          <span className="kiln-footer__note">
            A coding agent orchestrator for anywhere you are.
          </span>
        </div>
      </footer>
    </div>
  );
}
