// Marketing landing page (`/landing`). A standalone, scrolling page that
// explains the product and shows it in action with REAL screenshots of the
// running app (frontend/public/shots/*.png, captured against a seeded local
// stack). It is NOT part of the app shell: it holds no state, opens no
// stream/mic, and mounts no provider — it only reuses the Kiln design tokens
// (src/styles/tokens.css) for its chrome, so light/dark comes for free and the
// page follows the OS theme via resolveTheme (src/theme.ts), exactly like `/`.
//
// The shots that have a light and a dark capture are served through <picture>
// with a prefers-color-scheme source, so each screenshot matches the page
// theme. The board is captured from `/debug`, which the app renders dark-only
// (theme.ts), so it ships a single dark shot framed as a screen.
import type { JSX } from 'react';
import { Link } from 'react-router-dom';
import '@/landing/Landing.css';

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

/** A single proposal card, close up — a real capture of the Accept affordance. */
function ProposalShot(): JSX.Element {
  return (
    <ThemedShot
      base="proposal"
      className="shot-card"
      alt="A proposal card in the feed with an Accept button."
    />
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

const LOOP_STEPS: { n: string; title: string; body: string }[] = [
  {
    n: '01',
    title: 'Say the work out loud',
    body: 'Talk to the orchestrator like a lead. It turns your request into a ticket and shapes the details with you.',
  },
  {
    n: '02',
    title: 'Agents build in the cloud',
    body: 'Once a ticket is ready, a coding agent pulls it into its own sandbox and starts writing, running, and committing code.',
  },
  {
    n: '03',
    title: 'It stays quiet',
    body: 'Routine progress lands in the activity feed for whenever you look. No pings, no noise — the paper stays calm.',
  },
  {
    n: '04',
    title: "You're pulled in only when needed",
    body: 'When an agent hits a real decision, the ticket goes Blocked and Kiln notifies you. Tap in, answer by voice, and it resumes.',
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
    title: 'The whole operation, one board.',
    body: 'Every ticket moves through Backlog, Developing, and Done. Work-in-progress is hard-capped at the number of free workers, so the queue can never run away from you. Fire marks what needs a human; calm greens mean it is handled.',
    shot: <BoardShot />,
  },
  {
    eyebrow: 'The activity feed',
    title: 'Quiet until it matters.',
    body: 'Your home screen is a backlog of notifications ordered by urgency: blockers pinned to the top, proposals next, then updates newest-first. A divider marks what is new since your last visit; the rest is history you can scroll back into.',
    shot: <PhoneFeedShot />,
    flip: true,
  },
  {
    eyebrow: 'Proposals',
    title: 'Approve work with a tap.',
    body: 'While a ticket is still being shaped it surfaces as a proposal. Tap Accept to queue it instantly — no round-trip through the model — or just talk to the orchestrator to amend or drop it.',
    shot: <ProposalShot />,
  },
];

export function Landing(): JSX.Element {
  return (
    <div className="kiln-landing">
      <header className="kiln-nav">
        <div className="kiln-nav__inner">
          <Link to="/landing" className="kiln-nav__brand" aria-label="Kiln home">
            <KilnGlyph size={28} />
            <span className="kiln-nav__wordmark">Kiln</span>
          </Link>
          <nav className="kiln-nav__links" aria-label="Primary">
            <a href="#how">How it works</a>
            <a href="#features">Features</a>
            <a href="#voice">Voice</a>
          </nav>
          <Link to="/" className="kiln-btn kiln-btn--primary kiln-nav__cta">
            Open the app
          </Link>
        </div>
      </header>

      <main>
        <section className="kiln-hero">
          <div className="kiln-hero__copy">
            <span className="kiln-eyebrow">Voice-driven agent orchestration</span>
            <h1 className="kiln-hero__title">
              Run a team of coding agents.{' '}
              <span className="kiln-accent">Manage them by voice.</span>
            </h1>
            <p className="kiln-hero__lead">
              Kiln is a cloud orchestrator for autonomous coding agents. They write, run, and commit
              code in the cloud while an orchestrator watches the board — and pulls you in, by
              voice, only when a real decision is needed.
            </p>
            <div className="kiln-hero__actions">
              <Link to="/" className="kiln-btn kiln-btn--primary kiln-btn--lg">
                Open the app
              </Link>
              <a href="#how" className="kiln-btn kiln-btn--ghost kiln-btn--lg">
                See how it works
              </a>
            </div>
            <ul className="kiln-hero__chips" aria-label="At a glance">
              <li>Live board</li>
              <li>Activity feed</li>
              <li>Voice control</li>
              <li>Push when blocked</li>
            </ul>
          </div>
          <div className="kiln-hero__art">
            <PhoneFeedShot />
          </div>
        </section>

        <section id="how" className="kiln-section kiln-loop">
          <div className="kiln-section__head">
            <span className="kiln-eyebrow">The loop</span>
            <h2 className="kiln-section__title">Hands-off until you are genuinely needed.</h2>
            <p className="kiln-section__lead">
              Kiln is built to minimise interruption. Agents work silently; you step in for the
              decisions that actually move the work forward.
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

        <section id="features" className="kiln-section kiln-features">
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

        <section id="voice" className="kiln-section kiln-voice">
          <div className="kiln-voice__inner">
            <div className="kiln-voice__copy">
              <span className="kiln-eyebrow">Voice</span>
              <h2 className="kiln-section__title">Just talk.</h2>
              <p className="kiln-section__lead">
                Speak a message and the orchestrator reasons about it, then answers back — out loud.
                Ask for status, redirect an agent mid-flight, create or reprioritise tickets, or
                clear a blocker, all without typing. Kiln confirms anything destructive or ambiguous
                before it acts, so a mis-hear never quietly wrecks your work.
              </p>
              <div className="kiln-voice__actions">
                <Link to="/" className="kiln-btn kiln-btn--primary kiln-btn--lg">
                  Start talking
                </Link>
              </div>
            </div>
            <div className="kiln-voice__art">
              <DockShot />
            </div>
          </div>
        </section>

        <section className="kiln-cta">
          <div className="kiln-cta__inner">
            <KilnGlyph size={44} />
            <h2>Put your agents to work.</h2>
            <p>Open Kiln, say what you need, and let the orchestrator run the room.</p>
            <Link to="/" className="kiln-btn kiln-btn--primary kiln-btn--lg">
              Open the app
            </Link>
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
            <Link to="/">App</Link>
            <a href="#how">How it works</a>
            <a href="#features">Features</a>
          </nav>
          <span className="kiln-footer__note">
            A cloud orchestrator for autonomous coding agents.
          </span>
        </div>
      </footer>
    </div>
  );
}
