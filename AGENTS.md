# Kiln — Agent Working Agreement

This repo is built largely by coding agents. See `docs/specs/01-initial.md` (product) and
`docs/specs/02-initial-technical-architecture.md` (how it's built).

## Start here

1. Read the `end-to-end-development` skill (the gate + how to land a change), then your area's skill.
2. Install the gate hooks once: `make hooks` (pre-commit = lint+typecheck, pre-push = full gate).
3. Run the gate before you commit: `make check` (lint → type-check/build → unit + integration).
4. Bring the stack up locally: `make up` (see the `local-environment` skill).

## Ground rules

- **The hard gate is a wall.** Linters + type-check + tests must be green before you commit or
  merge. Red means you cannot land. Do not weaken a check to make it pass.
- **Test your work thoroughly** 
- **Work behind interfaces.** Each backend module (`backend/internal/{api,runtime,brain,board,agent}`)
  talks to its neighbors through an explicit contract. Stay inside your area's boundary.
- **The wire contract lives in `/schema`.** Never hand-edit generated types — change the schema and
  regenerate both Go and TS.
- **Update your area's skill as you work.** Each surface area has a skill under `.agents/skills`.
  When you learn something about your area — spec detail, a gotcha, how to run it — write it into
  that skill so the next agent inherits it.
- **Never `cat` (or otherwise dump) the `.env` file.** It holds the live secrets needed to run the
  app — LLM, Amika, and database credentials. Let the app/config loader read it; never print its
  contents to the transcript.

- **Commit directly to main.** You work in a development sandbox created for you. `main` is
  intentionally open on this repo — you are free to, and expected to, commit your work directly to
  `main`. There is no protected-branch or PR-review step to wait on. Just make sure the hard gate
  is green first.

## Working with the orchestrator

You run inside a sandbox created for development. A human orchestrator hands you work as tickets and
communicates with you through **blockers**.

- **Tickets are how work arrives.** The human provides tickets that describe what to build. Pick up
  your ticket and drive it to completion.
- **Enter a blocker when you need a decision.** When you hit a technical decision that needs the
  orchestrator's input — an ambiguous requirement, a design trade-off with no clear right answer, a
  missing credential, anything you can't resolve on your own — stop and return to the orchestrator
  in a **blocker** state rather than guessing.
- **The orchestrator understands "blocker".** Surfacing a blocker is the expected way to ask for
  input; the orchestrator recognizes the blocker state and responds. Blockers are the two-way
  channel: the human reaches you through them, and you reach the human through them.

## Useful Context

- **Not in production** This app is not used by anyone. No need to add feature flags or support backward comptability at this stage

## Skills

Canonical skills live in `.agents/skills` (symlinked into `.claude/skills` and `.codex/skills`).
Read your area's skill before working; keep it current as you go. Every agent starts from the two
general skills.
