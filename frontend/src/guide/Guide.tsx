// The onboarding guide as a styled webpage (`/guide`). A standalone, scrolling
// long-form page — the web rendering of docs/onboarding.md. Like the marketing
// page (Landing2) it is NOT part of the app shell: it holds no state, opens no
// stream/mic, and mounts no provider. It only reuses the Kiln design tokens
// (src/styles/tokens.css) for its chrome, so light/dark comes for free and the
// page follows the OS theme via resolveTheme (src/theme.ts), exactly like `/`.
//
// The screenshots in the source doc are placeholders (they need a live Kiln
// instance to capture), so rather than ship broken <img>s this page frames each
// one as a captioned placeholder describing exactly what to capture — the same
// intent as the markdown's figure captions.
import type { JSX } from 'react';
import { Link } from 'react-router-dom';
import '@/guide/Guide.css';

/** The Kiln bell mark (public/kiln-mark.svg), inlined so this page depends on no
 * shared component and can tint the glyph with the accent token. */
function KilnGlyph({ size = 28 }: { size?: number }): JSX.Element {
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

/** A captioned placeholder standing in for a screenshot. `caption` is the
 * "Figure N — …" line; `capture` describes what to shoot from a live instance. */
function Figure({ caption, capture }: { caption: string; capture: string }): JSX.Element {
  return (
    <figure className="guide-figure">
      <div className="guide-figure__frame" aria-hidden="true">
        <span className="guide-figure__badge">Screenshot</span>
        <p className="guide-figure__capture">{capture}</p>
      </div>
      <figcaption className="guide-figure__caption">{caption}</figcaption>
    </figure>
  );
}

/** The table-of-contents entries mirror the on-page section ids. */
const TOC: { href: string; label: string }[] = [
  { href: '#setup', label: 'Setting up Kiln' },
  { href: '#daily', label: 'Using Kiln day to day' },
  { href: '#how', label: 'How Kiln works' },
  { href: '#next', label: 'Where to go next' },
];

/** "What to have ready" — the pre-setup checklist. */
const CHECKLIST: { need: string; forWhat: string; where: JSX.Element }[] = [
  {
    need: 'A GitHub account (allowlisted)',
    forWhat: 'Signing in; connecting your repo',
    where: <>You already have it — ask your admin to be added to the allowlist</>,
  },
  {
    need: 'A repository URL',
    forWhat: 'The single repo your agents will work in',
    where: <>The GitHub repo you want Kiln to build in</>,
  },
  {
    need: 'An Anthropic API key',
    forWhat: "Powers the orchestrator's brain (Claude)",
    where: (
      <>
        <a href="https://console.anthropic.com" target="_blank" rel="noreferrer">
          console.anthropic.com
        </a>{' '}
        → API keys
      </>
    ),
  },
  {
    need: 'A GitHub token (PAT)',
    forWhat: 'Lets agents clone, read, and push to your repo',
    where: (
      <>
        GitHub → Settings → Developer settings → Personal access tokens (grant <code>repo</code>{' '}
        access)
      </>
    ),
  },
  {
    need: 'An Amika API key + credential ID',
    forWhat: 'Provisions and runs the cloud sandboxes agents work in',
    where: <>Your Amika console</>,
  },
];

/** The verification status marks shown beside each credential field. */
const STATUS_MARKS: { mark: string; markClass: string; meaning: string }[] = [
  { mark: '✓', markClass: 'is-ok', meaning: 'Verified — this connection works' },
  { mark: '✗', markClass: 'is-fail', meaning: 'Failed — hover it to read the error' },
  { mark: '…', markClass: 'is-pending', meaning: 'Checking (a save or verify is in flight)' },
  { mark: '—', markClass: 'is-none', meaning: 'Not verified yet' },
];

/** The five board states, grouped into the three on-screen columns. */
const BOARD_STATES: { column: string; zone: string; state: string; meaning: string }[] = [
  {
    column: 'Backlog',
    zone: 'Shaping',
    state: 'shaping',
    meaning: 'You and the orchestrator are still agreeing on the details.',
  },
  {
    column: 'Backlog',
    zone: 'Ready',
    state: 'ready',
    meaning: 'The details are settled; the ticket is eligible to be picked up.',
  },
  {
    column: 'Developing',
    zone: 'Working',
    state: 'working',
    meaning: 'An agent is actively working the ticket in a sandbox.',
  },
  {
    column: 'Developing',
    zone: 'Blocked',
    state: 'blocked',
    meaning: 'The agent stopped and the ticket is waiting on your decision.',
  },
  {
    column: 'Done',
    zone: '—',
    state: 'done',
    meaning: 'You accepted the result; the sandbox is released.',
  },
];

/** The everyday loop, day to day. */
const DAILY_LOOP: { title: string; body: JSX.Element }[] = [
  {
    title: 'You describe work',
    body: <>e.g. “Build a login form and wire it to the auth endpoint.”</>,
  },
  {
    title: 'Kiln proposes a ticket',
    body: (
      <>
        It shapes your request into a proposal card in the feed. You review it and{' '}
        <strong>Accept</strong> (or reply to refine it). See <a href="#shaping">Shaping</a>.
      </>
    ),
  },
  {
    title: 'Work starts on its own',
    body: (
      <>
        When an agent is free, Kiln automatically picks up the accepted ticket and an agent begins.
        You don’t dispatch anything by hand.
      </>
    ),
  },
  {
    title: 'You step in only when asked',
    body: (
      <>
        If an agent needs a decision, its ticket becomes <strong>Blocked</strong>, pinned to the top
        of your feed with the reason in full, and Kiln notifies you. You answer, and the agent
        resumes.
      </>
    ),
  },
  {
    title: 'Work finishes',
    body: (
      <>
        Kiln accepts the result and the ticket moves to Done, freeing that agent for the next piece
        of work.
      </>
    ),
  },
];

/** "Where to go next" — pointers into the specs. */
const NEXT_LINKS: { label: string; note: string }[] = [
  {
    label: 'Product design (the board and the orchestrator)',
    note: 'docs/specs/01-initial.md',
  },
  {
    label: 'Board mechanics (states, the pull, invariants)',
    note: 'docs/specs/03-board-mechanics.md',
  },
  { label: 'The v1 client you use', note: 'docs/specs/07-v1-text-client.md' },
  {
    label: 'How the feed surfaces proposals, blockers, and updates',
    note: 'docs/specs/08-user-interaction.md',
  },
  { label: 'The orchestrator brain', note: 'docs/specs/06-orchestrator-brain.md' },
];

/** The board flow, as a styled diagram (the web rendering of the doc's ASCII
 * art): Backlog → Developing → Done. */
function BoardFlow(): JSX.Element {
  return (
    <div className="guide-flow" aria-hidden="true">
      <div className="guide-flow__stage guide-flow__stage--backlog">
        <span className="guide-flow__col">Backlog</span>
        <div className="guide-flow__states">
          <span className="guide-flow__state">shaping</span>
          <span className="guide-flow__arrow">→</span>
          <span className="guide-flow__state">ready</span>
        </div>
      </div>
      <div className="guide-flow__link">
        <span className="guide-flow__link-arrow">↓</span>
        <span className="guide-flow__link-note">an agent is free → the system pulls it in</span>
      </div>
      <div className="guide-flow__stage guide-flow__stage--developing">
        <span className="guide-flow__col">Developing</span>
        <div className="guide-flow__states">
          <span className="guide-flow__state">working</span>
          <span className="guide-flow__arrow">⇄</span>
          <span className="guide-flow__state guide-flow__state--blocked">blocked</span>
        </div>
      </div>
      <div className="guide-flow__link">
        <span className="guide-flow__link-arrow">↓</span>
        <span className="guide-flow__link-note">you accept the result</span>
      </div>
      <div className="guide-flow__stage guide-flow__stage--done">
        <span className="guide-flow__state guide-flow__state--done">done</span>
      </div>
    </div>
  );
}

export function Guide(): JSX.Element {
  return (
    <div className="kiln-guide">
      <header className="guide-nav">
        <div className="guide-nav__inner">
          <Link to="/landing" className="guide-nav__brand" aria-label="Kiln home">
            <KilnGlyph size={28} />
            <span className="guide-nav__wordmark">Kiln</span>
          </Link>
          <nav className="guide-nav__links" aria-label="Guide sections">
            {TOC.map((entry) => (
              <a href={entry.href} key={entry.href}>
                {entry.label}
              </a>
            ))}
          </nav>
          <Link to="/dashboard" className="guide-btn guide-btn--primary guide-nav__cta">
            Open Kiln
          </Link>
        </div>
      </header>

      <div className="guide-layout">
        <aside className="guide-toc" aria-label="On this page">
          <span className="guide-toc__label">On this page</span>
          <ol className="guide-toc__list">
            {TOC.map((entry) => (
              <li key={entry.href}>
                <a href={entry.href}>{entry.label}</a>
              </li>
            ))}
          </ol>
        </aside>

        <main className="guide-main">
          <section className="guide-hero">
            <span className="guide-eyebrow">Onboarding guide</span>
            <h1 className="guide-hero__title">
              Getting started with <span className="guide-accent">Kiln</span>.
            </h1>
            <p className="guide-hero__lead">
              Kiln is a <strong>cloud orchestrator for autonomous coding agents.</strong> You run
              several coding agents in the cloud and manage them by talking to a single orchestrator
              through a mobile-first web app. You describe what you want, the orchestrator turns it
              into work and dispatches agents to do it, and you stay hands-off until an agent
              genuinely needs a decision from you.
            </p>
            <p className="guide-hero__sub">
              This guide takes you from zero to a working project step by step, then explains how
              Kiln works under the hood so the board you’re looking at actually makes sense.
            </p>
          </section>

          {/* ── Part 1 — Setting up Kiln ──────────────────────────── */}
          <section id="setup" className="guide-section">
            <span className="guide-eyebrow">Part 1</span>
            <h2 className="guide-section__title">Setting up Kiln</h2>

            <h3 className="guide-h3">Before you start: what to have ready</h3>
            <p>
              Kiln is <strong>invite-only</strong> in v1 — you sign in with GitHub, and your GitHub
              account must be on the allowlist. Beyond that, gather these before you begin so setup
              goes in one pass:
            </p>
            <div className="guide-table-wrap">
              <table className="guide-table">
                <thead>
                  <tr>
                    <th>You’ll need</th>
                    <th>What it’s for</th>
                    <th>Where to get it</th>
                  </tr>
                </thead>
                <tbody>
                  {CHECKLIST.map((row) => (
                    <tr key={row.need}>
                      <th scope="row">{row.need}</th>
                      <td>{row.forWhat}</td>
                      <td>{row.where}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <p>
              Keep these in reach — you’ll paste the keys during setup, and Kiln verifies each one
              live before you rely on it.
            </p>

            <ol className="guide-steps">
              <li className="guide-step" id="step-1">
                <span className="guide-step__num">1</span>
                <div className="guide-step__body">
                  <h3 className="guide-step__title">Sign in with GitHub</h3>
                  <p>
                    Open Kiln in your browser and go to the dashboard (<code>/dashboard</code>).
                    You’ll see a single centered card:
                  </p>
                  <ul className="guide-list">
                    <li>
                      The <strong>Kiln</strong> wordmark
                    </li>
                    <li>
                      A <strong>Continue with GitHub</strong> button
                    </li>
                  </ul>
                  <Figure
                    caption="Figure 1 — The sign-in screen (/dashboard when signed out)."
                    capture='The centered card with the "Continue with GitHub" affordance.'
                  />
                  <p>
                    Click <strong>Continue with GitHub</strong>. The browser leaves the app, you
                    authorize with GitHub, and you’re returned to the dashboard. (If your account
                    isn’t on the allowlist, sign-in won’t complete — contact whoever runs your Kiln
                    instance.)
                  </p>
                </div>
              </li>

              <li className="guide-step" id="step-2">
                <span className="guide-step__num">2</span>
                <div className="guide-step__body">
                  <h3 className="guide-step__title">Create your project</h3>
                  <p>
                    The first time you sign in you have no project yet, so you land on{' '}
                    <strong>“Set up your project.”</strong> Fill in the form:
                  </p>
                  <p className="guide-label">Required</p>
                  <ul className="guide-list">
                    <li>
                      <strong>Project name</strong> — what to call this board.
                    </li>
                    <li>
                      <strong>Repo URL</strong> — the repository your agents will work in (e.g.{' '}
                      <code>https://github.com/you/your-repo</code>).
                    </li>
                  </ul>
                  <p className="guide-label">Optional (sensible defaults apply if left blank)</p>
                  <ul className="guide-list">
                    <li>
                      <strong>Amika snapshot</strong> — the pre-built sandbox image agents start
                      from.
                    </li>
                    <li>
                      <strong>Brain model</strong> — the model that runs the orchestrator.
                    </li>
                    <li>
                      <strong>Worker count</strong> — how many agents can be working{' '}
                      <strong>at the same time</strong>. This is a hard cap and it matters more than
                      it looks — see <a href="#worker-count">Why worker count is a real limit</a>.
                    </li>
                  </ul>
                  <div className="guide-callout">
                    <p className="guide-callout__label">Sandbox secrets (optional)</p>
                    <p>
                      Secrets injected into every sandbox this project starts. The name is the
                      environment variable it lands under; the value is stored encrypted and never
                      shown again.
                    </p>
                  </div>
                  <p>
                    Click <strong>Add secret</strong> for each one (a name and a value), or{' '}
                    <strong>Remove</strong> to drop a row. Then press <strong>Save project</strong>.
                  </p>
                  <Figure
                    caption="Figure 2 — The first-run project form."
                    capture='The full form with the "Sandbox secrets" fieldset and the "Save project" button.'
                  />
                  <p>
                    Once the project saves, the dashboard swaps this screen for{' '}
                    <strong>Settings</strong> automatically — no navigation needed.
                  </p>
                </div>
              </li>

              <li className="guide-step" id="step-3">
                <span className="guide-step__num">3</span>
                <div className="guide-step__body">
                  <h3 className="guide-step__title">Add and verify your credentials</h3>
                  <p>
                    Settings is where your credentials live. It opens with your{' '}
                    <strong>account card</strong> (your GitHub avatar, name, and a{' '}
                    <strong>Sign out</strong> button) at the top, followed by the credential fields:
                  </p>
                  <ul className="guide-list">
                    <li>Anthropic API key</li>
                    <li>Amika API key</li>
                    <li>GitHub token</li>
                    <li>Amika Claude credential ID</li>
                  </ul>
                  <p>
                    These fields <strong>auto-save</strong> — there’s no “Save credentials” button.
                    Type a value and either press <strong>Enter</strong> or click away from the
                    field; that one field saves on its own. Saving any of the three secret keys
                    immediately kicks off a <strong>live verification</strong>, so a status mark
                    appears to the right of each field:
                  </p>
                  <div className="guide-table-wrap">
                    <table className="guide-table guide-table--marks">
                      <thead>
                        <tr>
                          <th>Mark</th>
                          <th>Meaning</th>
                        </tr>
                      </thead>
                      <tbody>
                        {STATUS_MARKS.map((row) => (
                          <tr key={row.meaning}>
                            <td>
                              <span className={`guide-mark ${row.markClass}`}>{row.mark}</span>
                            </td>
                            <td>{row.meaning}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                  <p>
                    The keys are <strong>write-only</strong>: once saved, the field never shows the
                    value again — only a masked placeholder like <code>configured · …x4Kd</code>.
                    Leaving a field blank keeps whatever was already stored, so you never have to
                    re-type a key you didn’t change.
                  </p>
                  <Figure
                    caption="Figure 3 — Credentials in Settings after verification."
                    capture="The credential fields showing ✓ marks, plus the account card above them."
                  />
                  <p>
                    <strong>Wait for all three checks to go green</strong> before moving on. A red ✗
                    means that connection won’t work at runtime — fix the key and it re-verifies as
                    you go.
                  </p>
                </div>
              </li>

              <li className="guide-step" id="step-4">
                <span className="guide-step__num">4</span>
                <div className="guide-step__body">
                  <h3 className="guide-step__title">
                    Turn on notifications <span className="guide-optional">(optional)</span>
                  </h3>
                  <p>
                    Below the credentials is a <strong>Notifications</strong> section. If your
                    browser supports it, click <strong>Enable notifications</strong> so Kiln can
                    reach you when a ticket needs a decision while the app is closed. You can skip
                    this and enable it later.
                  </p>
                </div>
              </li>

              <li className="guide-step" id="step-5">
                <span className="guide-step__num">5</span>
                <div className="guide-step__body">
                  <h3 className="guide-step__title">Open Kiln on your phone</h3>
                  <p>Kiln is mobile-first. The Settings page ends with a reminder:</p>
                  <div className="guide-callout">
                    <p>
                      Open kiln on your phone at this URL — the app itself doesn’t need sign-in yet.
                    </p>
                  </div>
                  <p>
                    Open the same URL’s home screen (<code>/</code>) on your phone. If setup is
                    complete you’ll see your board and the message dock. If you land on a card that
                    says <em>“Almost there — connect a project to light the kiln,”</em> your project
                    isn’t saved yet — tap <strong>Finish setup on your dashboard</strong> and finish
                    Step 2.
                  </p>
                  <p>You’re set up. From here on, you drive Kiln by talking to the orchestrator.</p>
                </div>
              </li>

              <li className="guide-step" id="step-6">
                <span className="guide-step__num">6</span>
                <div className="guide-step__body">
                  <h3 className="guide-step__title">
                    Install Kiln on your iPhone{' '}
                    <span className="guide-optional">(recommended)</span>
                  </h3>
                  <p>
                    Kiln is a web app, but on iPhone you can add it to your Home Screen so it opens
                    full-screen like a native app — no Safari address bar, and it’s one tap away.
                    Using Kiln installed this way is the intended experience, and it’s required for
                    push notifications to reach you when the app is closed.
                  </p>
                  <p>
                    In <strong>Safari</strong> on your iPhone, with Kiln open:
                  </p>
                  <ol className="guide-list guide-list--ordered">
                    <li>
                      Tap the <strong>Share</strong> button — the square-with-an-up-arrow icon in
                      the toolbar (bottom of the screen on iPhone, top on iPad).
                    </li>
                    <li>
                      In the share sheet, scroll down and tap <strong>Add to Home Screen</strong>.
                    </li>
                    <li>
                      Tap <strong>Install as Web App</strong> (shown as <strong>Add</strong> on
                      older iOS versions) to confirm. You can edit the name first if you like.
                    </li>
                  </ol>
                  <Figure
                    caption="Figure 5 — Installing Kiln on iPhone."
                    capture='The Safari Share sheet showing the "Add to Home Screen" option, and the confirm screen with "Install as Web App".'
                  />
                  <p>
                    Kiln now appears as an icon on your Home Screen. Open it from there — you’ll
                    stay signed in, and (once you enabled notifications in Step 4) blockers can
                    reach you even when the app isn’t open.
                  </p>
                  <div className="guide-callout guide-callout--note">
                    <p>
                      <strong>Note:</strong> This must be done in <strong>Safari</strong> — Chrome
                      and other iOS browsers don’t offer “Add to Home Screen.” Apple’s own
                      walkthrough is here:{' '}
                      <a
                        href="https://support.apple.com/guide/iphone/bookmark-favorite-webpages-iph42ab2f3a7/ios#iph4f9a47bbc"
                        target="_blank"
                        rel="noreferrer"
                      >
                        Add a website to your Home Screen on iPhone
                      </a>
                      .
                    </p>
                  </div>
                </div>
              </li>
            </ol>
          </section>

          {/* ── Part 2 — Using Kiln day to day ────────────────────── */}
          <section id="daily" className="guide-section">
            <span className="guide-eyebrow">Part 2</span>
            <h2 className="guide-section__title">Using Kiln day to day</h2>
            <p>
              The home screen (<code>/</code>) is built around a <strong>feed</strong> and a{' '}
              <strong>message dock</strong>:
            </p>
            <ul className="guide-list">
              <li>
                The <strong>feed</strong> (middle) is where Kiln talks to you: proposals to review,
                blockers that need a decision, and progress updates. Seen items stay as scrollable
                history — nothing disappears just because you looked at it.
              </li>
              <li>
                The <strong>message dock</strong> (bottom) is where you talk to Kiln. Type a message
                and press <strong>Enter</strong> (voice dictation is available where supported).
                Kiln replies in the feed — it never speaks back to you.
              </li>
            </ul>
            <Figure
              caption="Figure 4 — The home screen (/)."
              capture="Header, feed with a proposal or blocker card, and the message dock."
            />
            <p>The everyday loop looks like this:</p>
            <ol className="guide-loop">
              {DAILY_LOOP.map((step, i) => (
                <li className="guide-loop__step" key={step.title}>
                  <span className="guide-loop__num">{String(i + 1).padStart(2, '0')}</span>
                  <div>
                    <h3 className="guide-loop__title">{step.title}</h3>
                    <p>{step.body}</p>
                  </div>
                </li>
              ))}
            </ol>
            <p>
              You can give input <strong>at any time</strong> — not only in response to a blocker.
              Redirect an agent mid-flight, add or reprioritize tickets, or just ask for status.
              It’s all handled through the same message dock.
            </p>
          </section>

          {/* ── Part 3 — How Kiln works under the hood ────────────── */}
          <section id="how" className="guide-section">
            <span className="guide-eyebrow">Part 3</span>
            <h2 className="guide-section__title">How Kiln works under the hood</h2>
            <p>
              You never move work through Kiln by hand. You have a conversation with the{' '}
              <strong>orchestrator</strong>, and it manipulates a <strong>board</strong> of{' '}
              <strong>tickets</strong> on your behalf. Those three pieces — the orchestrator,
              tickets, and the board — are the whole system.
            </p>

            <h3 className="guide-h3">The orchestrator</h3>
            <p>
              The orchestrator is Kiln’s <strong>brain</strong>: the single thing you talk to. It’s{' '}
              <em>event-driven</em>, not a background loop — it wakes on an event, looks at the
              current board, decides what should change, and goes back to sleep. There are two kinds
              of event:
            </p>
            <ul className="guide-list">
              <li>
                <strong>you giving input</strong> (a message you send), and
              </li>
              <li>
                <strong>an agent finishing a turn</strong>.
              </li>
            </ul>
            <p>
              On each event the brain can create a ticket, shape it, mark it ready, send
              instructions to a working agent, or accept a finished result. It never writes code
              itself — coding agents do the work; the orchestrator manages the flow and talks to
              you.
            </p>

            <h3 className="guide-h3">Tickets</h3>
            <p>
              A <strong>ticket</strong> is one unit of work — one thing you want done. It starts as
              something you said, carries a title, a body (the details, which grow as it’s shaped),
              and a priority. A ticket is never in two places at once: at any moment it’s in exactly
              one of five <strong>states</strong>, and that state is the single source of truth for
              where it is and what’s happening to it.
            </p>
            <p>In your feed, tickets show up as cards:</p>
            <ul className="guide-list">
              <li>
                a <strong>proposal</strong> while it’s being shaped (review and accept it),
              </li>
              <li>
                a <strong>blocker</strong> when it needs your decision (pinned to the top), and
              </li>
              <li>
                <strong>updates</strong> as it makes progress.
              </li>
            </ul>

            <h3 className="guide-h3" id="shaping">
              Shaping
            </h3>
            <p>
              <strong>Shaping</strong> is the conversation that turns a vague ask into a
              well-defined ticket — and it’s the gate where <em>you</em> have the last word before
              any agent starts.
            </p>
            <p>
              When you first describe something, the orchestrator captures it as a ticket in the
              Shaping state and turns it into a <strong>proposal</strong> in your feed. From there
              you either:
            </p>
            <ul className="guide-list">
              <li>
                <strong>Accept it</strong> — tapping Accept is an instant, mechanical action (no AI
                round-trip); the ticket becomes <em>ready</em> and joins the queue, or
              </li>
              <li>
                <strong>Refine it</strong> — reply with changes (“also validate the DB schema”,
                “decline this”) and the orchestrator reshapes or drops it.
              </li>
            </ul>
            <p>
              Shaping matters because agents work from the ticket, not from your head. The clearer a
              ticket is when it leaves shaping, the more likely the agent gets it right on the first
              turn — and the less likely it bounces back to you asking for clarification.
            </p>

            <h3 className="guide-h3">The board: how tickets flow through states</h3>
            <p>
              The board groups the five states into <strong>three columns</strong>, with the first
              two split into stacked <strong>zones</strong>:
            </p>
            <div className="guide-table-wrap">
              <table className="guide-table">
                <thead>
                  <tr>
                    <th>Column</th>
                    <th>Zone</th>
                    <th>State</th>
                    <th>What it means</th>
                  </tr>
                </thead>
                <tbody>
                  {BOARD_STATES.map((row) => (
                    <tr key={row.state}>
                      <td>{row.column}</td>
                      <td>{row.zone}</td>
                      <td>
                        <code>{row.state}</code>
                      </td>
                      <td>{row.meaning}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <p>
              A thing worth internalizing:{' '}
              <strong>“Backlog” and “Developing” are columns, not steps.</strong> The real machine
              has five states — <em>shaping, ready, working, blocked, done</em> — and the
              columns/zones are just how those states are grouped on screen. “Backlog” is simply
              where a ticket lives before an agent picks it up.
            </p>
            <p>Here’s the full flow, top to bottom:</p>
            <BoardFlow />
            <p>Step by step:</p>
            <ol className="guide-list guide-list--ordered">
              <li>
                <strong>shaping</strong> — You describe what you want; the orchestrator creates the
                ticket and shapes it with you (above).
              </li>
              <li>
                <strong>ready</strong> — Once you accept the proposal, the orchestrator marks it
                ready. It’s now eligible to be worked, but nothing starts it yet.
              </li>
              <li>
                <strong>working</strong> — This transition is special: it is <strong>not</strong> a
                decision anyone makes. The system runs a <strong>deterministic pull</strong> — the
                moment a <em>ready</em> ticket exists <em>and</em> an agent is free, the
                highest-priority ready ticket is automatically pulled into development and an agent
                starts. You can’t force it and neither can the orchestrator; it just happens when
                the conditions are met.
              </li>
              <li>
                <strong>blocked</strong> — When an agent’s turn ends needing a human decision (a
                question, an ambiguity, or a failure), the ticket moves to Blocked and Kiln notifies
                you. It <strong>keeps its sandbox</strong> — Blocked is a pause <em>inside</em>{' '}
                Developing, not a trip back to the backlog. You answer, the orchestrator relays it,
                and the ticket returns to Working. A ticket can bounce between Working and Blocked
                as many times as the work needs.
              </li>
              <li>
                <strong>done</strong> — When the orchestrator accepts the result, the ticket moves
                to Done and its agent is released — which may immediately trigger the pull to start
                the next ready ticket.
              </li>
            </ol>
            <p>
              There are no other moves: no cancel, no delete, no demoting a ready ticket, no
              reopening a done one. Every arrow above drives a real action — moving a ticket isn’t
              rearranging a card, it dispatches an agent, sends a notification, or releases a
              sandbox.
            </p>

            <h3 className="guide-h3">Blocked, and how you stay in the loop</h3>
            <p>
              You’re only pulled in when a decision genuinely needs you — that’s the{' '}
              <strong>Blocked</strong> zone. When a ticket blocks, Kiln pins a blocker card to the
              top of your feed with the full reason and notifies you, so you don’t have to sit
              watching the board. You reply with the answer, and the orchestrator resumes the agent
              in the same sandbox, with all its context intact. Everything else — picking up ready
              work, running turns, moving cards — happens without you.
            </p>

            <h3 className="guide-h3" id="worker-count">
              Why worker count is a real limit
            </h3>
            <p>
              The <strong>worker count</strong> you set during setup is the hard cap on how many
              tickets can be in the Developing column at once. Each agent occupies one slot the
              entire time its ticket is Working <em>or</em> Blocked, and only releases it on Done.
            </p>
            <p>
              This is what makes the automatic pull predictable. Ready tickets don’t all start at
              once — they queue, and whenever a slot frees, the <strong>highest-priority</strong>{' '}
              ready ticket is pulled in next. Accept five proposals with three workers, and three
              start while two wait. That’s why priority matters, and why finishing work (accepting
              to Done) is what unblocks the next piece.
            </p>
          </section>

          {/* ── Where to go next ──────────────────────────────────── */}
          <section id="next" className="guide-section">
            <h2 className="guide-section__title">Where to go next</h2>
            <ul className="guide-next">
              {NEXT_LINKS.map((link) => (
                <li className="guide-next__item" key={link.note}>
                  <span className="guide-next__label">{link.label}</span>
                  <code className="guide-next__path">{link.note}</code>
                </li>
              ))}
            </ul>
          </section>
        </main>
      </div>

      <footer className="guide-footer">
        <div className="guide-footer__inner">
          <div className="guide-footer__brand">
            <KilnGlyph size={22} />
            <span>Kiln</span>
          </div>
          <nav className="guide-footer__links" aria-label="Footer">
            <Link to="/landing">Home</Link>
            <Link to="/dashboard">Open Kiln</Link>
            <a href="#setup">Back to top</a>
          </nav>
          <span className="guide-footer__note">
            A coding agent orchestrator for anywhere you are.
          </span>
        </div>
      </footer>
    </div>
  );
}
