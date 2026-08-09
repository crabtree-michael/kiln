# Ticket draft — `/tests/README.md` describes voice behaviour that was reversed, and a CI lane that doesn't exist

Drafted 2026-08-09 while cleaning up `.agents/skills/*/SKILL.md` (docs-only). **Out of scope for
that ticket** — the skills pass deliberately did not edit `/tests`, and the skills now point at
this README as the authoritative e2e recipe, which makes these two lines worth correcting.

Paste the title/body below into the board; the rest is working detail.

---

## Title

`/tests/README.md` documents mic-on-by-default and a keyless CI workflow, and neither is true

## Body

Two stale claims in `/tests/README.md`. Both are in prose the skills now redirect readers to, so
they are read more often than they were.

**1. Mic-on-by-default (the substantive one).** The `voice-mic-to-brain.spec.ts` entry says
opening the app runs "the real frontend pipeline (mic-on-by-default → worklet → AssemblyAI socket
→ commit machine)".

The mic has **not** been on by default since 09 D3 was reversed. The app opens *Paused* ("Tap to
talk") and the mic starts **only** on an explicit tap of the mic control — no start on mount, no
foreground resume. The `voice-pipeline` skill has the correct account, and the spec itself taps
"Talk" to start the mic (that click is also the user gesture the AudioContext needs). So the
README contradicts both the skill and the spec it is describing.

Why it matters beyond tidiness: someone debugging a silent voice e2e reads "mic-on-by-default",
concludes the tap is redundant, and removes the one line that actually starts the mic — or, worse,
"fixes" the app to match the README and reintroduces the auto-start.

**2. The keyless CI workflow doesn't exist.** The README says "CI runs this lane on every push and
PR (`.github/workflows/e2e-keyless.yml`)". `.github/workflows/` contains **only `check.yml`**, and
`check.yml`'s own header says e2e "stays out: it needs a live stack". The keyless lane is
CI-*runnable* — that is the point of it — but nothing runs it today.

Either wire the workflow up or soften the claim to "CI-runnable; not yet wired". Prefer wiring it
up: the lane exists precisely so the live loop can be exercised without keys, and a documented-but-
absent gate is worse than no gate, because it is read as coverage.

## The change

- `tests/README.md`, the `voice-mic-to-brain.spec.ts` bullet: replace "mic-on-by-default" with the
  real sequence — tap "Talk" → worklet → AssemblyAI socket → commit machine.
- `tests/README.md`, the keyless section: either add `.github/workflows/e2e-keyless.yml`, or state
  that the lane is CI-runnable and not currently wired.

## Not in scope

The `voice-pipeline` and `end-to-end-development` skills are already correct on both points and
need no change.
