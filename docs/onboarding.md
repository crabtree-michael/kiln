# Getting started with Kiln

Kiln is a **cloud orchestrator for autonomous coding agents.** You run several
coding agents in the cloud and manage them by talking to a single orchestrator
through a mobile-first web app. You describe what you want, the orchestrator turns
it into work and dispatches agents to do it, and you stay hands-off until an agent
genuinely needs a decision from you.

This guide takes you from zero to a working project step by step, then explains
how Kiln works under the hood so the board you're looking at actually makes sense.

> **About the screenshots.** The images below are placeholders. Capturing real
> screenshots requires a running Kiln instance with GitHub sign-in and live
> credentials. Each placeholder's caption describes exactly what to capture, so
> they can be filled in from a live instance. Drop the PNGs in
> `docs/images/onboarding/` to match the paths referenced here.

---

## Part 1 — Setting up Kiln

### Before you start: what to have ready

Kiln is **invite-only** in v1 — you sign in with GitHub, and your GitHub account
must be on the allowlist. Beyond that, gather these before you begin so setup goes
in one pass:

| You'll need | What it's for | Where to get it |
| --- | --- | --- |
| A **GitHub account** (allowlisted) | Signing in; connecting your repo | You already have it — ask your admin to be added to the allowlist |
| A **repository URL** | The single repo your agents will work in | The GitHub repo you want Kiln to build in |
| A **GitHub token** (PAT) | Lets agents clone, read, and push to your repo | GitHub → Settings → Developer settings → Personal access tokens (grant `repo` access) |
| An **Amika API key** + **credential ID** | Provisions and runs the cloud sandboxes agents work in | Your Amika console |

Keep these in reach — you'll paste the keys during setup, and Kiln verifies each
one live before you rely on it.

---

### Step 1 — Sign in with GitHub

Open Kiln in your browser and go to the dashboard at
[trykiln.dev/dashboard](https://trykiln.dev/dashboard). You'll see a
single centered card:

- The **Kiln** wordmark
- A **Continue with GitHub** button

![Sign-in screen with the Kiln wordmark and a "Continue with GitHub" button](images/onboarding/01-sign-in.png)
*Figure 1 — The sign-in screen (`/dashboard` when signed out). Capture: the centered card with the "Continue with GitHub" affordance.*

Click **Continue with GitHub**. The browser leaves the app, you authorize with
GitHub, and you're returned to the dashboard. (If your account isn't on the
allowlist, sign-in won't complete — contact whoever runs your Kiln instance.)

### Step 2 — Create your project

The first time you sign in you have no project yet, so you land on **"Set up your
project."** Fill in the form:

**Required**

- **Project name** — what to call this board.
- **Repo URL** — the repository your agents will work in (e.g.
  `https://github.com/you/your-repo`).

**Optional (sensible defaults apply if left blank)**

- **Amika snapshot** — the pre-built sandbox image agents start from.
- **Brain model** — the model that runs the orchestrator.
- **Worker count** — how many agents can be working **at the same time**. This is a
  hard cap and it matters more than it looks — see [Why worker count is a real
  limit](#why-worker-count-is-a-real-limit).

**Sandbox secrets (optional)**

Under **Sandbox secrets**, add any environment variables your agents' sandboxes
need (API keys the code itself calls, etc.). The on-screen hint says it plainly:

> Secrets injected into every sandbox this project starts. The name is the
> environment variable it lands under; the value is stored encrypted and never
> shown again.

Click **Add secret** for each one (a name and a value), or **Remove** to drop a
row. Then press **Save project**.

![The "Set up your project" form showing Project name, Repo URL, and the optional fields plus the Sandbox secrets section](images/onboarding/02-create-project.png)
*Figure 2 — The first-run project form. Capture: the full form with the "Sandbox secrets" fieldset and the "Save project" button.*

Once the project saves, the dashboard swaps this screen for **Settings**
automatically — no navigation needed.

### Step 3 — Add and verify your credentials

Settings is where your credentials live. It opens with your **account card** (your
GitHub avatar, name, and a **Sign out** button) at the top, followed by the
credential fields:

- **Amika API key**
- **GitHub token**
- **Amika Claude credential ID**

These fields **auto-save** — there's no "Save credentials" button. Type a value
and either press **Enter** or click away from the field; that one field saves on
its own. Saving either of the two secret keys immediately kicks off a **live
verification**, so a status mark appears to the right of each field:

| Mark | Meaning |
| --- | --- |
| **✓** | Verified — this connection works |
| **✗** | Failed — hover it to read the error |
| **…** | Checking (a save or verify is in flight) |
| *(nothing)* | Not verified yet |

The keys are **write-only**: once saved, the field never shows the value again —
only a masked placeholder like `configured · …x4Kd`. Leaving a field blank keeps
whatever was already stored, so you never have to re-type a key you didn't change.

![The Settings page: account card at top, then Amika / GitHub credential fields each with a green checkmark to the right](images/onboarding/03-credentials.png)
*Figure 3 — Credentials in Settings after verification. Capture: the credential fields showing ✓ marks, plus the account card above them.*

**Wait for both checks to go green** before moving on. A red ✗ means that
connection won't work at runtime — fix the key and it re-verifies as you go.

### Step 4 — Open Kiln on your phone

Kiln is mobile-first. The Settings page ends with a reminder:

> Open kiln on your phone at trykiln.dev — the app itself doesn't need sign-in yet.

Open the home screen at [trykiln.dev](https://trykiln.dev) on your phone. If setup
is complete you'll see your board and the message dock. If you land on a card that
says *"Almost there — connect a project to light the kiln,"* your project isn't
saved yet — tap **Finish setup on your dashboard** and finish Step 2.

You're set up. From here on, you drive Kiln by talking to the orchestrator.

### Step 5 — Install Kiln on your iPhone (recommended)

Kiln is a web app, but on iPhone you can add it to your Home Screen so it opens
full-screen like a native app — no Safari address bar, and it's one tap away.
Using Kiln installed this way is the intended experience, and it's required for
push notifications to reach you when the app is closed.

In **Safari** on your iPhone, with Kiln open:

1. Tap the **Share** button — the square-with-an-up-arrow icon in the toolbar
   (bottom of the screen on iPhone, top on iPad).
2. In the share sheet, scroll down and tap **Add to Home Screen**.
3. Tap **Install as Web App** (shown as **Add** on older iOS versions) to confirm.
   You can edit the name first if you like.

![The iOS Safari Share sheet with the "Add to Home Screen" row highlighted](images/onboarding/05-add-to-home-screen.png)
*Figure 5 — Installing Kiln on iPhone. Capture: the Safari Share sheet showing the "Add to Home Screen" option, and the confirm screen with "Install as Web App".*

Kiln now appears as an icon on your Home Screen. Open it from there — you'll stay
signed in, and (once you turn on notifications in the next step) blockers can reach
you even when the app isn't open.

> **Note:** This must be done in **Safari** — Chrome and other iOS browsers don't
> offer "Add to Home Screen." Apple's own walkthrough is here:
> [Add a website to your Home Screen on iPhone](https://support.apple.com/guide/iphone/bookmark-favorite-webpages-iph42ab2f3a7/ios#iph4f9a47bbc).

### Step 6 — Turn on notifications

Set up notifications **after** you've installed Kiln on your Home Screen (Step 5)
and opened it from there — on iPhone, push can only reach you from the installed
app, not from a Safari tab. So save this for last, once Kiln is running from its
Home Screen icon.

In the header, tap the **bell** icon to open the notification settings dropdown,
then tap **Enable notifications** and allow the permission prompt when your phone
asks. Kiln can now reach you when a ticket needs a decision while the app is
closed. The same dropdown lets you choose *when* to be notified:

- **Blocked** (the default) — a notification only when a ticket needs your decision.
- **All updates** — a notification on every feed update (handy for testing).

You can change the mode, or turn notifications back off, from the bell dropdown at
any time.

---

## Part 2 — Using Kiln day to day

The home screen (`/`) is built around a **feed** and a **message dock**:

- The **feed** (middle) is where Kiln talks to you: proposals to review, blockers
  that need a decision, and progress updates. Seen items stay as scrollable
  history — nothing disappears just because you looked at it.
- The **message dock** (bottom) is where you talk to Kiln. Type a message and
  press **Enter** (voice dictation is available where supported). Kiln replies in
  the feed — it never speaks back to you.

![The home screen: a header with a tickets status menu, a scrollable feed of cards in the middle, and the message dock at the bottom](images/onboarding/04-home-feed.png)
*Figure 4 — The home screen (`/`). Capture: header, feed with a proposal or blocker card, and the message dock.*

The everyday loop looks like this:

1. **You describe work** — e.g. *"Build a login form and wire it to the auth
   endpoint."*
2. **Kiln proposes a ticket** — it shapes your request into a proposal card in the
   feed. You review it and **Accept** (or reply to refine it). See
   [Shaping](#shaping).
3. **Work starts on its own** — when an agent is free, Kiln automatically picks up
   the accepted ticket and an agent begins. You don't dispatch anything by hand.
4. **You step in only when asked** — if an agent needs a decision, its ticket
   becomes **Blocked**, pinned to the top of your feed with the reason in full,
   and Kiln notifies you. You answer, and the agent resumes.
5. **Work finishes** — Kiln accepts the result and the ticket moves to Done,
   freeing that agent for the next piece of work.

You can give input **at any time** — not only in response to a blocker. Redirect
an agent mid-flight, add or reprioritize tickets, or just ask for status. It's all
handled through the same message dock.

---

## Part 3 — How Kiln works under the hood

You never move work through Kiln by hand. You have a conversation with the
**orchestrator**, and it manipulates a **board** of **tickets** on your behalf.
Those three pieces — the orchestrator, tickets, and the board — are the whole
system.

### The orchestrator

The orchestrator is Kiln's **brain**: the single thing you talk to. It's
*event-driven*, not a background loop — it wakes on an event, looks at the current
board, decides what should change, and goes back to sleep. There are two kinds of
event:

- **you giving input** (a message you send), and
- **an agent finishing a turn**.

On each event the brain can create a ticket, shape it, mark it ready, send
instructions to a working agent, or accept a finished result. It never writes code
itself — coding agents do the work; the orchestrator manages the flow and talks to
you.

### Tickets

A **ticket** is one unit of work — one thing you want done. It starts as something
you said, carries a title, a body (the details, which grow as it's shaped), and a
priority. A ticket is never in two places at once: at any moment it's in exactly
one of five **states**, and that state is the single source of truth for where it
is and what's happening to it.

In your feed, tickets show up as cards:

- a **proposal** while it's being shaped (review and accept it),
- a **blocker** when it needs your decision (pinned to the top), and
- **updates** as it makes progress.

### Shaping

**Shaping** is the conversation that turns a vague ask into a well-defined ticket —
and it's the gate where *you* have the last word before any agent starts.

When you first describe something, the orchestrator captures it as a ticket in the
Shaping state and turns it into a **proposal** in your feed. From there you either:

- **Accept it** — tapping Accept is an instant, mechanical action (no AI round-trip);
  the ticket becomes *ready* and joins the queue, or
- **Refine it** — reply with changes ("also validate the DB schema", "decline
  this") and the orchestrator reshapes or drops it.

Shaping matters because agents work from the ticket, not from your head. The
clearer a ticket is when it leaves shaping, the more likely the agent gets it right
on the first turn — and the less likely it bounces back to you asking for
clarification.

### The board: how tickets flow through states

The board groups the five states into **three columns**, with the first two split
into stacked **zones**:

| Column | Zone | State | What it means |
| --- | --- | --- | --- |
| **Backlog** | Shaping | `shaping` | You and the orchestrator are still agreeing on the details. |
| **Backlog** | Ready | `ready` | The details are settled; the ticket is eligible to be picked up. |
| **Developing** | Working | `working` | An agent is actively working the ticket in a sandbox. |
| **Developing** | Blocked | `blocked` | The agent stopped and the ticket is waiting on *your* decision. |
| **Done** | — | `done` | You accepted the result; the sandbox is released. |

A thing worth internalizing: **"Backlog" and "Developing" are columns, not steps.**
The real machine has five states — *shaping, ready, working, blocked, done* — and
the columns/zones are just how those states are grouped on screen. "Backlog" is
simply where a ticket lives before an agent picks it up.

Here's the full flow, left to right:

```
     you describe work
            │
            ▼
 ┌────────────────────────┐
 │  Backlog                │
 │   shaping  →  ready     │
 └────────────────────────┘
            │  an agent is free → the system pulls it in (automatic)
            ▼
 ┌────────────────────────┐
 │  Developing             │
 │   working  ⇄  blocked   │
 └────────────────────────┘
            │  you accept the result
            ▼
        ┌────────┐
        │  done  │
        └────────┘
```

Step by step:

1. **shaping** — You describe what you want; the orchestrator creates the ticket
   and shapes it with you (above).
2. **ready** — Once you accept the proposal, the orchestrator marks it ready. It's
   now eligible to be worked, but nothing starts it yet.
3. **working** — This transition is special: it is **not** a decision anyone makes.
   The system runs a **deterministic pull** — the moment a *ready* ticket exists
   *and* an agent is free, the highest-priority ready ticket is automatically
   pulled into development and an agent starts. You can't force it and neither can
   the orchestrator; it just happens when the conditions are met.
4. **blocked** — When an agent's turn ends needing a human decision (a question, an
   ambiguity, or a failure), the ticket moves to Blocked and Kiln notifies you. It
   **keeps its sandbox** — Blocked is a pause *inside* Developing, not a trip back
   to the backlog. You answer, the orchestrator relays it, and the ticket returns
   to Working. A ticket can bounce between Working and Blocked as many times as the
   work needs.
5. **done** — When the orchestrator accepts the result, the ticket moves to Done
   and its agent is released — which may immediately trigger the pull to start the
   next ready ticket.

There are no other moves: no cancel, no delete, no demoting a ready ticket, no
reopening a done one. Every arrow above drives a real action — moving a ticket
isn't rearranging a card, it dispatches an agent, sends a notification, or releases
a sandbox.

### Blocked, and how you stay in the loop

You're only pulled in when a decision genuinely needs you — that's the **Blocked**
zone. When a ticket blocks, Kiln pins a blocker card to the top of your feed with
the full reason and notifies you, so you don't have to sit watching the board. You
reply with the answer, and the orchestrator resumes the agent in the same sandbox,
with all its context intact. Everything else — picking up ready work, running
turns, moving cards — happens without you.

### Why worker count is a real limit

The **worker count** you set during setup is the hard cap on how many tickets can
be in the Developing column at once. Each agent occupies one slot the entire time
its ticket is Working *or* Blocked, and only releases it on Done.

This is what makes the automatic pull predictable. Ready tickets don't all start at
once — they queue, and whenever a slot frees, the **highest-priority** ready ticket
is pulled in next. Accept five proposals with three workers, and three start while
two wait. That's why priority matters, and why finishing work (accepting to Done)
is what unblocks the next piece.

---

## Where to go next

- **Product design (the board and the orchestrator):**
  [`docs/specs/01-initial.md`](specs/01-initial.md)
- **Board mechanics (states, the pull, invariants):**
  [`docs/specs/03-board-mechanics.md`](specs/03-board-mechanics.md)
- **The v1 client you use:** [`docs/specs/07-v1-text-client.md`](specs/07-v1-text-client.md)
- **How the feed surfaces proposals, blockers, and updates:**
  [`docs/specs/08-user-interaction.md`](specs/08-user-interaction.md)
- **The orchestrator brain:** [`docs/specs/06-orchestrator-brain.md`](specs/06-orchestrator-brain.md)
