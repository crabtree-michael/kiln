# Design: Private repository support

**Date:** 2026-07-12
**Status:** proposed
**Scope:** `internal/repo`, `internal/agent/amika` (and the neutral `agent` provider config), `cmd/kiln` (provider factories + repo-shell wiring), `internal/identity/verify` (shared clone-auth helper), plus onboarding/UI copy. Optional, non-breaking `schema/openapi.yaml` enrichment. No new env vars, no new secret storage, no OAuth scope change.

---

## 1. Problem & objective

Kiln can only work against **public** GitHub repositories today. Two independent repository accesses exist, and the git paths in both are unauthenticated:

1. **Brain's host-side clone** (`internal/repo`) — a maintained local clone under `KILN_REPO_DIR/<projectID>` used read-only by the orchestrator brain to search and to run the merge gate (`VerifyOnMain` / `VerifyInPR`). The clone and `git fetch origin` are **deliberately unauthenticated**; the per-project token is passed as `GH_TOKEN` **only** to `gh` (`repo.go:333-347`, `repo.go:398-414`). A private repo fails at `git clone` and at every `git fetch`.
2. **Worker's sandbox clone** (Amika/Devin) — Kiln hands the provider a bare `repo_url`; the provider's remote sandbox clones it and the coding agent commits/pushes from inside (`agent/amika/client.go:213-236`). Kiln passes no git credential, so a private repo cannot be cloned in the sandbox.

The credential and the intent already exist: onboarding instructs the user to supply a GitHub PAT with `repo` scope "so agents can clone, read, and push" (`docs/onboarding.md:32`), the token is stored per-user and encrypted (`user_config.github_auth_token_enc`), decrypted per project into `RuntimeConfig.GitHubAuthToken` (`identity/service.go:429`), and the reachability check `Verifier.VerifyRepo` already authenticates via `AuthedCloneURL` (`identity/verify/verify.go:142-202`). Private-repo support is the last mile: **wire the already-stored per-project token into the two git paths that currently ignore it, for both public and private repos, without leaking it.**

**Objective:** a project whose `repo_url` points at a private GitHub repository works end-to-end — the brain can clone/fetch/search it, the coding agent can clone/push it, and both merge gates verify against it — using the project owner's existing GitHub token, with no regression for public repos and no new user-facing configuration beyond the token the flow already asks for.

### Non-goals

- Changing the sign-in OAuth flow to request `repo` scope. Identity and repo access stay **deliberately decoupled** (spec 11 §2, D2); the PAT remains the repo credential.
- GitHub App installation tokens, SSH deploy keys, per-project (vs per-user) credentials, or multiple repos per project. See [Deferred](#9-deferred).

---

## 2. Design principle: visibility is implicit, never a flag

There is **no `private` boolean** anywhere — not in `projects`, not on the wire, not in the UI. Access is governed entirely by the presence and validity of a token:

- **Token present** → every git operation (host clone/fetch, sandbox clone) authenticates with it. This works for private repos and is harmless for public ones.
- **Token absent** → git operations stay anonymous exactly as today (public read still works; `gh` and push still require a token, as they already do).

This keeps the "switch between public and private" UX to what already exists — the `repo_url` field and the `github_auth_token` field — and makes the two halves of private support ("where the repo is" and "the credential that reaches it") the same two fields the dashboard already renders (`ConfigFields.tsx` Repo URL + GitHub token; the token's verify check is already labelled `"repo"`). A public→private switch is: point `repo_url` at the private repo and ensure the token grants access; the `"repo"` verify check confirms reachability. A private→public switch just works, with or without a token.

The single credential — the owner's PAT — becomes the git credential for **all** repo access: host clone/fetch, sandbox clone/push, and `gh`/PR verification. That is the design's spine.

---

## 3. Current-state map (verified)

| Access | Where | Operation | Credential today | Private-repo status |
|---|---|---|---|---|
| Brain host clone | `repo.go:339` `clone()` | `git clone --filter=blob:none <url> <dir>` | **none** (token kept out of origin by design) | ✗ fails |
| Brain fetch (main gate + bash) | `repo.go:252`, `repo.go:144` | `git fetch origin`, arbitrary `git`/`rg` | **none** | ✗ fails |
| PR gate | `repo.go:287` `verifyInPR` | `gh api repos/{owner}/{repo}/commits/<sha>/pulls` | `GH_TOKEN` = `rc.GitHubAuthToken` | ✓ works if token has PR read |
| Worker clone + push | Amika sandbox, via `client.go:213` `CreateWorker` → `POST /sandboxes {repo_url}` | clone + agent commits/pushes, inside the sandbox | **none from Kiln** (repo_url only) | ✗ fails to clone; push already needs a cred |
| Repo reachability check | `identity/verify/verify.go:142` `VerifyRepo` | `git ls-remote <authed-url>` | `AuthedCloneURL(url, token)` | ✓ already authenticated |

Key structures:

- Credential resolution: `identity/service.go:405-432` `RuntimeConfig()` → project → owner user → `user_config`, decrypting `GitHubTokenEnc` into `RuntimeConfig.GitHubAuthToken` (plaintext, in-process only; never logged or wired).
- Host repo shell construction (per project): `cmd/kiln/wiring.go:325-332` `buildTenantProviders` builds `repo.New(repo.Config{RepoURL, AuthToken: rc.GitHubAuthToken, Dir: cfg.RepoDir+"/"+pid})`. `AuthToken` reaches only `GH_TOKEN` today.
- Amika provider construction (per project): `cmd/kiln/registry.go:72-82` `buildAmikaProvider` maps `rc.Project.RepoURL` into `amika.Config.RepoURL`; **the token is not passed.**
- Existing auth helper: `identity/verify/verify.go:192-202` `AuthedCloneURL(repoURL, token)` → `https://x-access-token:<token>@<host>/…` (GitHub PAT / fine-grained convention; non-https or empty-token passes through unchanged).
- Invalidation: a settings/project write calls `identity`'s invalidator → `tenant.Registry.Invalidate` (`wiring.go:228`), dropping the cached providers so the next event rebuilds with fresh config. The on-disk clone at `RepoDir/<projectID>` is **reused** across rebuilds (`tenant/registry.go` `Providers.Close` doc).

---

## 4. Approach

### 4.1 Authenticate the brain's host clone/fetch without leaking the token

The existing invariant — *keep the token out of the persisted `origin` remote so it never appears in `git remote -v` or the brain's logs* (`repo.go:333-338`) — is worth preserving. Embedding `x-access-token:…@` in the clone URL (as `AuthedCloneURL` does for the one-shot `ls-remote` probe) would bake the token into the on-disk remote and is therefore **rejected** for the maintained host clone.

Instead, authenticate via a per-invocation git config injected through the environment, so the token lives only in process memory and in the git command's ephemeral config — never on disk (git's global/system config are already routed to `/dev/null`, `repo.go:404-405`), never in the URL, and never echoed in git error output.

Extend `runEnv` (`repo.go:398-414`) so that **when `AuthToken != ""` and `RepoURL` is https**, git is told to send an `Authorization` header for the repo's host:

```
http.https://<host>/.extraheader = Authorization: Basic base64("x-access-token:" + token)
```

Implementation notes:
- Derive `<host>` from `cfg.RepoURL` (e.g. `github.com`, or a GHE host) so the header is scoped to the repo's origin and never sent elsewhere. Key the config on `http.https://<host>/.extraheader`.
- This is layered onto the existing `GIT_CONFIG_COUNT`/`GIT_CONFIG_KEY_n`/`GIT_CONFIG_VALUE_n` mechanism: keep `credential.helper=` (empty, still disabling host keychain helpers) and add the extraheader as the next indexed pair; bump `GIT_CONFIG_COUNT`.
- The header covers `clone`, `fetch` (main gate), on-demand `--filter=blob:none` blob fetches, and any `git` the brain runs through `Run`. `gh` keeps reading `GH_TOKEN` unchanged.
- **`clone()` still clones the plain `RepoURL`** — origin stays token-less; the extraheader supplies auth. No change to the persisted remote.

Because the token is applied fresh from `runEnv` on every invocation and is *not* baked into the clone, **token rotation takes effect on the next operation with no re-clone needed** — a settings write invalidates the tenant providers, the shell is rebuilt over the same clone dir, and the next `fetch` uses the new token.

This is the GitHub-Actions-standard `extraheader` pattern; it improves on URL-embedding because git never echoes the header in error output (unlike a URL, which `VerifyRepo` must currently discard output to hide, `verify.go:150-151`).

### 4.2 Fix repo-URL staleness on the maintained clone (switch-repos correctness)

Today `repo.New` leaves an existing clone untouched if `Dir` is already a git repo (`repo.go:116`). If a project's `repo_url` **changes** (including a public→private swap to a different repo), the maintained clone keeps pointing at the old `origin` and silently serves stale content — a latent bug that private-repo switching makes user-visible. Add to `New`:

- If `Dir` is already a git repo, read `git remote get-url origin`; if it does not match `cfg.RepoURL`, either `git remote set-url origin <RepoURL>` + `git fetch`, or wipe `Dir` and re-clone. Prefer set-url + fetch (cheap, preserves object cache).

This is scoped in because "UX for switching between public and private repos" is an explicit deliverable and the two changes touch the same `New` path.

### 4.3 Authenticate the worker's sandbox clone/push

Kiln hands the provider a `repo_url`; the provider's sandbox performs the actual clone, and the coding agent commits/pushes from inside. For a private repo the sandbox needs a git credential; for **any** repo, push already needs one — so the owner's PAT is the natural single git credential for the sandbox.

Thread the token into the provider config and let each adapter map it to the provider's mechanism.

**Neutral layer:** the `agent.Provider` port stays repo-agnostic (spec 05 §2, multi-provider design). The token is passed at construction, resolved in the factory from `rc.GitHubAuthToken` — it never enters the port surface.

**Amika adapter (`internal/agent/amika`):**
- Add `RepoToken string` to `amika.Config` (`client.go:75-96`).
- In `CreateWorker` (`client.go:213-236`), when `RepoToken != ""` and `RepoURL` is https, authenticate the sandbox's git access. Two candidate mechanisms — **the exact one must be confirmed against the live v0beta1 contract (see §7 VERIFY), consistent with the existing `secret_env_vars` "verify on first live run" caveat (`amika/types.go:20-27`):**
  1. Send the authenticated clone URL as `repo_url` (`AuthedCloneURL(cfg.RepoURL, cfg.RepoToken)`), so the sandbox clones with the credential embedded, and **also** inject the raw token as a secret env var (e.g. `GH_TOKEN`) via the existing `secret_env_vars` path so the agent's `gh` and `git push` authenticate. This is the most self-contained option and mirrors how push must already be working.
  2. If Amika exposes a first-class git-credential field on `POST /sandboxes`, prefer that (keeps the token out of the sandbox's origin remote).
- Whichever is chosen, the token reaches the sandbox through Amika's secret channel (TLS + provider secret storage), never through a URL logged by Kiln.

**Wiring:** `cmd/kiln/registry.go` `buildAmikaProvider` sets `RepoToken: d.Runtime.GitHubAuthToken`.

**Devin adapter:** Devin authenticates repositories through its own GitHub connection/integration (the owner links GitHub inside Devin), not through a Kiln-supplied PAT on the session-create call. For v1, **document** that Devin private-repo access depends on the owner's Devin↔GitHub connection; if the Devin API later accepts a per-session git credential, thread `rc.GitHubAuthToken` the same way in `buildDevinProvider`. No Kiln code change is required to *not break* Devin.

### 4.4 Reachability check — already private-capable

`Verifier.VerifyRepo` already probes with `AuthedCloneURL` (`verify.go:142-159`), so the dashboard's `"repo"` check already turns green for a reachable private repo when the token grants access, and red with a scrubbed `exit N` message when it does not. **Optional enrichment** (§6): when the check succeeds, one extra `gh api repos/{owner}/{repo} --jq .private` call can annotate the result message as `repo reachable · private` / `· public`, giving the user positive confirmation of what they connected. This is the only candidate wire change and is non-breaking.

### 4.5 Shared clone-auth helper

`AuthedCloneURL` currently lives in `internal/identity/verify`. It is now used by verify **and** the amika adapter (option 1), and the base64 header builder in §4.1 is a close cousin. Relocate `AuthedCloneURL` (and add a small `AuthHeader(token) string` / host-deriver) into a neutral shared package — e.g. `internal/repo` (already the git-auth home) or a tiny `internal/githelper` — and have `verify` and `amika` depend on it. Keep the "result carries a live secret; never log or return it" contract on both helpers.

---

## 5. Configuration & setup

**No new environment variables and no new secret storage.** Private support reuses:

- `KILN_SECRETS_KEY` — AES-256-GCM master key for `github_auth_token_enc` (unchanged).
- The per-user GitHub token, set write-only via `PUT /api/settings { github_auth_token }` and surfaced only as a `SecretStatus` fingerprint (unchanged).
- `KILN_REPO_DIR` — per-project clone root (unchanged).
- `githubapi` base URLs are already overridable for GitHub Enterprise (`githubapi/client.go:28-54`); the §4.1 host-derivation makes the extraheader GHE-correct automatically.

**Token scope guidance (documentation deliverable).** For a private repo the token must grant private-repo read/write and PR read:
- **Fine-grained PAT (recommended):** scope to the *single* target repository with **Contents: read & write** (clone + push), **Pull requests: read** (PR merge gate), **Metadata: read**. Smallest blast radius.
- **Classic PAT:** the `repo` scope. Note prominently that a classic `repo` PAT grants access to **all** of the user's repositories — prefer fine-grained.

Onboarding (`docs/onboarding.md`) and the dashboard token field helper text update to state the private-repo scope requirement and recommend fine-grained + single-repo.

---

## 6. API & wire changes

**None required.** `MeProject.repo_url` (public string) and `MeSettings.github_auth_token` (`SecretStatus`) already model the two halves; the backend behavior change in §4 is invisible to the contract.

**Optional, non-breaking enrichment** (only if we implement §4.4's visibility annotation): the `"repo"` `VerifyCheck.message` string gains a `· private` / `· public` suffix. This is a free-text message field already, so even this needs no schema edit. If any typed field is ever added, it goes through the wire-schema flow — edit `schema/openapi.yaml`, run `make schema`, commit both generated outputs (`backend/internal/wire/generated.go`, `frontend/src/schema/generated.ts`) atomically; never hand-edit generated files (`schema/README.md`, wire-schema skill).

**Permissions:** no change to the OAuth grant. Sign-in continues to request only default public identity; the PAT remains the sole repo credential (spec 11 §2, D2).

---

## 7. Security considerations

- **At rest:** the token is already AES-256-GCM encrypted per user under `KILN_SECRETS_KEY`; reads are fingerprint-only (`SecretStatus`), and no endpoint returns a stored secret. Unchanged.
- **In memory:** `RuntimeConfig.GitHubAuthToken` is plaintext, in-process only, never logged or placed on a wire type. Maintain — the new code paths (extraheader builder, `amika.Config.RepoToken`) must uphold this: never log the token, never include it in an error string.
- **Out of the persisted host remote:** §4.1 keeps the token out of `origin` (extraheader-via-env, not URL embedding), so `git remote -v`, the persisted config, and git error output never carry it — a strict improvement over URL-embedding.
- **Scrub git/gh output:** `repo.Run`'s combined output is capped and logged at info and can surface in the feed. The token already reaches the sandbox/brain env (`GH_TOKEN` today), so a *trusted* brain could already echo it — this is a pre-existing property, not a new class. Still, add defense-in-depth: scrub the token value and the base64 auth header from `repo.Result.Output` and from any structured log before it leaves the process. `VerifyRepo` already discards git output for exactly this reason (`verify.go:150-151`).
- **New trust boundary — transmitting the PAT to the provider (§4.3):** supporting private sandbox clones means Kiln sends the owner's repo-scoped token to a third-party sandbox provider (Amika) over its API. This is a real escalation from the public-repo posture and must be called out to operators/users. Mitigations: (a) transmit only over TLS via the provider's secret channel (`secret_env_vars` / a first-class credential field), never in a URL Kiln logs; (b) **strongly** recommend a fine-grained PAT scoped to the single repo to bound blast radius; (c) treat a GitHub App installation token (short-lived, auto-rotating, per-repo) as the preferred future credential (Deferred). Devin already holds its own GitHub connection, so this boundary is Amika-specific under option 1.
- **Blast radius of a leaked classic `repo` PAT:** all of the user's repos. Documentation must steer users to fine-grained single-repo tokens.
- **Rotation & revocation:** a settings write invalidates the tenant providers; because the host token is applied per-invocation (not baked into the clone), a rotated token takes effect on the next fetch, and a cleared token immediately returns git to anonymous. Sandbox workers pick up the new token on their next recreate (release/`AcceptToDone` destroys+recreates the sandbox).

---

## 8. Testing

- **Unit (`internal/repo`):** the `runEnv`/git-config builder emits the correct `http.https://<host>/.extraheader` with `Basic base64("x-access-token:"+token)` when a token is set for an https URL, and omits it when the token is empty or the URL is non-https; host derivation for `github.com` and a GHE host; `GIT_CONFIG_COUNT` incremented correctly and `credential.helper=` still present. Origin-mismatch detection in `New` (set-url + fetch vs re-clone).
- **Unit (shared helper):** `AuthedCloneURL` / `AuthHeader` pass-through for empty token and non-https; correct `x-access-token` shaping; secret never in a returned error.
- **Integration (brain clone against private):** stand up a local git-over-HTTP server requiring Basic auth (or a fixture behind an auth proxy); assert (a) clone + `git fetch origin` succeed with the token, (b) fail without it, (c) the token is **absent** from `git remote -v`, the on-disk config, and any captured `Run` output/log, (d) rotating the token succeeds on the next fetch with no re-clone, (e) `VerifyOnMain` fetches privately and gates correctly.
- **Provider (amika):** against the amika mock/httptest server, `CreateWorker` sends the chosen auth mechanism (authed `repo_url` and/or the `GH_TOKEN` secret env var) when `RepoToken` is set, and the plain `repo_url` when it is not. Add a `// VERIFY ON FIRST LIVE RUN` note pinning down which mechanism the live sandbox actually honors (mirrors `amika/types.go:20-27`).
- **Verify check:** `VerifyRepo` returns `ok` for a reachable private repo with a good token and `failed: git ls-remote failed: exit N` (no URL/token in the message) for a bad/absent token; optional `· private`/`· public` annotation when enabled.
- **Security regression:** token never appears in `Me`/`SecretStatus`, never in structured logs, never in `repo.Result.Output` after scrubbing.
- **E2e:** extend `tests/tests/brain-has-repo-access.spec.ts` (or add a sibling) to point at a private fixture repo and assert the brain can read it and the merge gate passes; keep the existing public-repo path green (no regression).

---

## 9. Deferred

- **GitHub App installation tokens** — short-lived, auto-rotating, per-repo credentials minted from an App installation. The strongly-preferred long-term replacement for long-lived PATs: smaller blast radius, no manual rotation, cleaner provider hand-off. Requires a GitHub App registration and an installation-token minting path; larger than this change.
- **OAuth `repo` scope at sign-in** — explicitly rejected to keep identity and repo access decoupled (spec 11 D2). Recorded here so the decision isn't relitigated.
- **SSH deploy keys** — an alternative to token auth (per-repo, revocable). Not needed while the PAT/App path covers HTTPS.
- **Per-project (vs per-user) repo credentials** — today credentials follow the person (spec 11 D4). Revisit when multi-project-per-user lands (drop of `one_project_per_owner`).
- **Devin per-session git credential** — thread `rc.GitHubAuthToken` into `buildDevinProvider` if/when the Devin API accepts one; until then Devin relies on its own GitHub connection.
- **Auto-detected visibility badge in the UI** — surface `public`/`private` from the GitHub API as a read-only dashboard indicator (builds on §4.4).

---

## 10. Decision log

- **D1 — Reuse the existing per-user PAT as the single git credential; no new secret.** The token is already stored, encrypted, decrypted per project, and asked for in onboarding with `repo` scope. Private support is wiring it into the two git paths that ignore it, not adding a credential.
- **D2 — Visibility is implicit; authenticate whenever a token is present.** No `private` flag in the DB, wire, or UI. Same `repo_url` + token fields cover public and private; the "switch" is transparent and the `"repo"` verify check is the source of truth for reachability.
- **D3 — Host clone authenticates via env-injected `http.extraheader`, not URL embedding.** Preserves the token-out-of-origin invariant, survives rotation without re-clone, and never echoes the token in git error output.
- **D4 — Sandbox clone/push authenticates through the provider's secret channel.** Token threaded via the adapter Config (`amika.Config.RepoToken`), mapped to the provider mechanism inside the adapter; the neutral `Provider` port stays repo-agnostic. The PAT→provider transmission is a documented trust boundary mitigated by fine-grained scoping.
- **D5 — Fix repo-URL staleness in `repo.New` as part of this change.** Switching repos (a stated deliverable) is broken today by clone reuse; correcting origin on mismatch belongs with the private-repo work in the same `New` path.
- **D6 — No breaking wire/API change.** The contract already models both halves; only backend behavior, provider wiring, a shared helper, and docs change. Any optional annotation stays within existing free-text fields or goes through `make schema`.
