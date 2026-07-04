// Package repo maintains a local git clone of the project repository and runs
// allowlisted shell commands inside it. It is the infrastructure behind the
// brain's read-oriented `bash` tool: verify that an agent pushed its work before
// a ticket is accepted, and search the code when a decision needs it.
//
// Design: docs/superpowers/specs/2026-07-04-brain-repo-bash-tool-design.md
// (Component 1). Consumer: 06 orchestrator-brain (§4 tool set) via its RepoShell
// port.
//
// Module boundary: the brain (internal/brain) is pure decision logic over narrow
// ports and must NOT import this package. It is wired in at the composition root
// (cmd/kiln) through a repoShellAdapter that copies repo.Result into the brain's
// own RepoResult shape, mirroring the AgentInspector read-seam (05 §2).
//
// Enforcement (design "Decisions"): commands run as `sh -c <command>` with PATH
// pointed at an allowed-bin directory of symlinks to just the whitelisted
// binaries (git gh rg find ls grep head tail sort uniq wc awk sed cat), plus a
// wall-clock timeout and an output cap. Full shell power (pipes, &&) but only
// whitelisted binaries are reachable — no rm, curl, etc. Boot is non-fatal: a
// missing/unconfigured repo or a failed clone yields a disabled Shell whose Run
// returns Result{Unavailable: true} instead of erroring the pass.
package repo
