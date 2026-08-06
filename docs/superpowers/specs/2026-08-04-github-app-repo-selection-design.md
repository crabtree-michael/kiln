# Design: user-selected repository access (OAuth App → GitHub App migration)

**Date:** 2026-08-04
**Status:** approved for build — migration confirmed 2026-08-05. **One decision blocks rollout, not implementation (§0).**
**Scope:** `internal/identity` (+ `githubapi`, `githubmock`, `verify`), `internal/repo`, `cmd/kiln` (config, wiring, bootstrap, registry), `schema/openapi.yaml` + both generated sides, the dashboard's onboarding step 1 and Integrations card, `render.yaml` / `docker-compose*.yml`, the keyless e2e stack.
**Amends:** spec `11` §2 (identity & auth). Supersedes the "Deferred → GitHub App installation tokens" entry in `2026-07-12-private-repository-support-design.md` §9.

---

## 0. Decision needed before rollout

> **Do existing connected users get prompted to install the App and re-select repos, or does this apply to new sign-ups only?**

Everything below can be built and merged without the answer — §6's fallback keeps every existing OAuth credential working untouched, so the migration is non-breaking either way. What the answer changes is one banner on Integrations and whether an install prompt is forced on next sign-in. It must be settled **before this reaches production users**, because the two answers imply different states for the same account and we should not ship a half-decided prompt.

Two other prerequisites are outside this repo and gate deployment, not coding: the App registration (§7) and its secrets in Render.

---

## 1. Problem & objective

Connecting GitHub at sign-up grants Kiln **blanket** access — every repository the account can reach, with no way for the user to narrow it. The ask is GitHub's standard "All repositories / Only select repositories" chooser, presented at sign-up and revisitable afterwards.

**Objective:** the user picks, on GitHub's own screen, which repositories Kiln may touch — and can change that choice later from Integrations without support involvement.

**Why it is a migration and not a feature flag:** per-repository selection is a **GitHub App installation** feature. OAuth Apps have no equivalent — `repo` is all-or-nothing per account, and the only narrowing on offer is org-level approval. Kiln connects as an OAuth App today (§2), so the chooser cannot exist until the connection itself changes.

### Non-goals

- Per-project (rather than per-user) credentials, or more than one repo per project.
- SSH deploy keys.
- Consuming GitHub webhooks (§4.6 explains why the App still declares none).
- Changing what the repo picker *means* in the UI. It still chooses which repo a project points at; it simply lists a set the user has already narrowed.

---

## 2. Current state (verified)

Kiln connects through a **GitHub OAuth App** requesting the `repo` scope:

| Fact | Where |
|---|---|
| `ScopeRepo = "repo"`, `AuthorizeURL` | `backend/internal/identity/githubapi/client.go:110-131` |
| `ConnectURL` requests it; `CompleteConnect` refuses a grant without it | `backend/internal/identity/service.go:108-145` |
| `GITHUB_OAUTH_CLIENT_ID` / `_SECRET`, boot gate | `backend/cmd/kiln/wiring.go:630-650` |
| Long-lived token stored encrypted in `user_config.github_auth_token_enc` | `backend/internal/identity/service.go:141`, `postgres/store.go` |
| Decrypted per project into `RuntimeConfig.GitHubAuthToken` | `backend/cmd/kiln/wiring.go:164,330` |
| Consumed as clone-URL auth and `GH_TOKEN` for `gh` | `backend/internal/repo/repo.go:39,402` |
| Reachability probe embeds it in a clone URL | `backend/internal/identity/verify/verify.go:142-202` |
| Repo picker lists `GET /user/repos` | `backend/internal/identity/githubapi/client.go:210-225` |

**The 2026-08-03 amendment did not already do this.** It collapsed a scopeless `/auth/github/login` and a repo-scoped `/auth/github/connect` into one route (`frontend/src/auth/github-connect.ts`; spec `11` §2). "Repo-scoped" there means *the `repo` OAuth scope*, not *repository selection*. It removed a footgun where a settings card silently granted no repo access; it did not narrow what an account grants.

**Where the credential actually goes — and does not.** The user's GitHub token is used only by in-process git/`gh` invocations inside the backend. The Amika sandbox receives the project's own configured `amika_secrets` (`backend/cmd/kiln/registry.go:80`), never the user's GitHub credential. This bounds the token-lifetime risk in §3.3 and is the single most load-bearing fact in this design.

---

## 3. Design

### 3.1 One App, two grants, one round trip

Register a GitHub App with **"Request user authorization (OAuth) during installation"** enabled. Installing it then does both jobs in one pass:

- **Installation** → GitHub renders the repository chooser and yields an `installation_id`.
- **User authorization** → the same `code` exchange Kiln already implements, yielding a user access token for `GET /user` and the allowlist check.

This is what lets the migration keep Kiln's existing identity model intact. Without it we would need a second trip to GitHub to learn who installed the App.

### 3.2 Route shape — one flow stays one flow

`GET /auth/github/connect` keeps its name and its "there is exactly one flow" invariant (`11` §2). Only its redirect target changes:

```
https://github.com/apps/<KILN_GITHUB_APP_SLUG>/installations/new?state=<nonce>
```

`GET /auth/github/callback` keeps verifying `state` and exchanging `code`, and additionally reads:

- `installation_id` — persisted against the user (§3.3).
- `setup_action` — `install` (first time) or `update` (returning from the Configure screen having changed the selection). Both are success; `update` skips the find-or-create and only refreshes the stored installation.

`GITHUB_CONNECT_PATH` and every affordance pointing at it are unchanged. The "one route, nothing left to pick wrong" property survives untouched — that is the point of routing the App flow through the existing name rather than adding `/auth/github/install`.

**Callback ordering.** GitHub may deliver the user-authorization `code` and the `installation_id` in the same request, but a user who installs from GitHub's own Marketplace/Apps page arrives with `installation_id` and no `code`. Handle both: `code` present → the full identity path; `code` absent but a valid session exists → attach the installation to the session's user; neither → send to sign-in.

### 3.3 Credential model — the structural change

A GitHub App issues no long-lived token. Kiln mints an **installation access token** on demand:

1. Build a short-lived RS256 JWT (`iss` = App ID, ≤10 min) signed with the App private key.
2. `POST /app/installations/{installation_id}/access_tokens`.
3. Receive a token valid for **one hour**.

So the storage flips: `user_config` holds an **installation id** (not a secret — it identifies, it does not authorize) where it held an encrypted token, and `RuntimeConfig.GitHubAuthToken string` becomes a **token source**:

```go
// identity.InstallationTokens — mints and caches per installation.
type TokenSource func(ctx context.Context) (string, error)
```

with an in-memory cache keyed by installation id, refreshed when the cached token is within ~5 minutes of expiry. Minting is a network call, so it must never sit behind a lock held across the request; a per-installation singleflight is the right shape.

**Rotation is the whole point.** The private key is the only long-lived secret, it lives in the backend and nowhere else, and a compromised installation token expires within the hour. This is strictly better than the current model, where a `repo`-scoped token with full account reach is stored indefinitely.

Call sites and what each needs:

| Site | Today | Under an App |
|---|---|---|
| `internal/repo` clone/fetch URL (`repo.go:39`) | fixed token baked into the URL at wiring | re-resolve per git invocation |
| `internal/repo` `GH_TOKEN` (`repo.go:402`) | fixed token in `runEnv()` | mint per `gh` invocation |
| `verify.AuthedCloneURL` (`verify.go:196`) | fixed token | mint per probe |
| Repo picker listing | `GET /user/repos` | `GET /user/installations/{id}/repositories` |

That last row is what makes the picker show **only the selected repos** — the user-visible payoff, and it falls out of the credential change rather than needing its own feature.

**The expiry hazard, stated plainly.** Any place that hands a token to a third party for longer than an hour holds a credential that dies mid-flight. Today there is no such place (§2), so this migration introduces no such bug. But it constrains the future: if the private-repo hand-off (`2026-07-12-private-repository-support-design.md` §4.3) is ever wired to inject the user's credential into a sandbox, that path needs a refresh story — a token minted at sandbox creation will be dead before a long agent turn pushes. Anyone implementing that must read this paragraph first.

### 3.4 Connection state

`GitHubConnection.Status` stops being derived from a scope string:

- `GitHubConnected` — an installation exists and its token mints.
- `GitHubNeedsReconnect` — installation missing, suspended, or revoked (GitHub answers `404`/`401` on the mint).
- `GitHubUnknownScopes` — unchanged. It remains the carry-forward slot for hand-typed PATs and OAuth-era tokens (§6). Its comment at `service.go:169-176` already promises nobody is pushed through a fresh grant by a refactor; this migration keeps that promise.

`grantsRepoScope` / `splitScopes` (`service.go:147-167`) stay for the fallback path only, and get a comment saying so — they are no longer the primary classifier.

### 3.5 Revisiting the choice

Integrations gains a **Configure on GitHub** link beside Connected:

```
https://github.com/settings/installations/<installation_id>
```

GitHub redirects org installations to the right org-scoped page, so one URL shape covers both. GitHub owns that screen and we link out rather than reimplementing it. Returning users re-enter through `/auth/github/connect` with `setup_action=update`, and the repo listing re-reads the installation, so an added or removed repo simply appears or disappears.

Copy change: `GITHUB_ACCESS_NOTE` (`frontend/src/dashboard/integrations-config.ts:35`) currently reads "grants Kiln read and write access to your repositories". It must now say Kiln gets access to *the repositories you choose on the next screen* — the note is the promise the chooser then keeps.

---

## 4. GitHub App permissions

Requested permissions are the user's consent screen. Two rules shape the list: ask for nothing a code path does not need, and understand that **adding a permission later forces every existing installation to re-approve** — a silent, per-user migration cost. The list below is set against the near-term roadmap, not only today's `git grep`.

### 4.1 Repository permissions

| Permission | Level | Why |
|---|---|---|
| **Metadata** | Read | Mandatory — GitHub requires it whenever any repository permission is granted. |
| **Contents** | Read & write | Read covers what runs today: the brain's maintained clone and every `git fetch` (`repo.go`). Write is requested now because the agent-push path is the documented direction of the private-repo design (§4.3 there), and adding it later re-prompts every user. |
| **Pull requests** | Read & write | Read is what Kiln itself calls: the merge gate runs `gh api repos/{owner}/{repo}/commits/<sha>/pulls` (`repo.go:287`), and the brain's prompt directs it to `gh pr list` (`brain/prompt.go:112`). Write is requested for the same forward-looking reason as Contents — the "have the agent open a PR" path in that same prompt, and any future PR comment. |
| **Issues** | *not requested* | See §4.2. |

**On Contents/PR write.** This is a deliberate trade against the ticket's own spirit of minimal access, and it should be made consciously rather than by default. The argument for granting now: both are named in shipped prompt text as things the system drives toward, and a later addition re-prompts everyone. The argument against: nothing in the codebase writes with Kiln's own credential today, so read-only would be strictly honest right now. **Recommendation: grant both as write.** Even at write, this is materially narrower than today's `repo` scope, which additionally carries issues, actions, webhooks, packages, deployments, and admin across *every* repo in the account.

### 4.2 Issues — recommended omission

No code path touches issues. `gh` is on the brain's bash allowlist (`repo.go:32`), so the brain *could* attempt `gh issue ...`; without the permission that call fails cleanly with a `403` the brain reads as a failed command, which is the correct outcome for a capability we have not designed.

Issues is also the permission most legible to a user as over-reach on a consent screen — "why does a coding orchestrator want my issues?" — and this whole ticket exists to make that screen trustworthy. Omit it. If we later give the brain an issues capability, that is the moment to add it, with the re-approval accepted as part of that feature's cost.

### 4.3 Account / organization permissions

**None.** Kiln reads no org membership. The one thing that looks like it does — `affiliation=owner,collaborator,organization_member` at `githubapi/client.go:266` — is a filter parameter on `/user/repos`, not an org permission, and it disappears anyway when the picker moves to `/user/installations/{id}/repositories` (§3.3).

### 4.4 Webhooks

**Inactive.** Kiln has no webhook receiver and every provider module polls by design (`agent/amika/client.go:30`, `agent/devin/client.go:37`). Leave the webhook disabled at registration; it is a per-App setting that can be turned on later without re-prompting users, unlike permissions. If we ever want push/PR events instead of polling, that is its own design with its own endpoint, secret verification, and replay story.

### 4.5 Installation target

**"Any account"** — users must be able to install on their own personal accounts and on orgs they administer. "Only this account" would restrict installation to the account that owns the App and make the product unusable for everyone else.

### 4.6 Summary for the registration form

- Repository permissions: Metadata **Read**, Contents **Read & write**, Pull requests **Read & write**.
- Organization permissions: none.
- Account permissions: none.
- Webhook: **inactive**.
- Where can this GitHub App be installed: **Any account**.
- Request user authorization (OAuth) during installation: **enabled** (§3.1).
- Callback URL: the existing `/auth/github/callback`.

---

## 5. Wire, schema & config

**Wire (`02` §3 — schema is the source, never hand-edit either generated side).** In `schema/openapi.yaml`, `GitHubConnection` loses `scopes` and gains an installation id and the configure URL; `MeSettings.github_auth_token` (a `SecretStatus`) is reinterpreted — something is "stored" when an installation exists. Regenerate `backend/internal/wire/generated.go` and `frontend/src/schema/generated.ts` together.

**Config.** New, replacing `GITHUB_OAUTH_CLIENT_ID` / `_SECRET`:

| Env | Notes |
|---|---|
| `KILN_GITHUB_APP_ID` | numeric App ID |
| `KILN_GITHUB_APP_SLUG` | the public link's slug — builds the install URL (§3.2) |
| `KILN_GITHUB_APP_PRIVATE_KEY` | The App private key. **Multiline secret** — decided 2026-08-06: carried as **base64 of the `.pem`**, one line, decoded at boot (§5.1). |
| `KILN_GITHUB_APP_CLIENT_ID` / `_CLIENT_SECRET` | the App's own OAuth credentials, for the user-authorization half |

The boot gate at `wiring.go:630-650` extends to these: a partial set must refuse boot exactly as it does today, so a half-configured deployment cannot serve a working-looking `/auth/github/connect` that dead-ends.

Touch `render.yaml`, `docker-compose.yml`, `docker-compose.keyless.yml`, and `docs/onboarding.md`.

### 5.1 The private key is base64, and boot sniffs which form it got

**Decision (2026-08-06):** `KILN_GITHUB_APP_PRIVATE_KEY` carries **base64 of the whole `.pem` file**, on one line — not the PEM itself, and not a path to a secret file.

Why base64 rather than a mounted file: the key has to reach three places that treat multiline values differently (a Render env var, a `docker-compose` env pass-through, a local `.env` that `source` and a dozen ad-hoc parsers both read), and one of them mangling newlines produces a key that parses locally and 401s in production — the intermittent-looking failure this whole file exists to avoid. One base64 line survives all three unchanged. A secret file would avoid the newline problem too, but it adds a mount to configure per environment and a second thing to get wrong; the key is one value, so it stays one value.

Boot decodes with a **sniff, not a flag**:

> A value whose leading non-whitespace is `-----BEGIN` is PEM verbatim; anything else is base64-decoded first, then parsed.

Both branches end at the existing `githubapi.ParsePrivateKey`, which already takes PEM bytes and already accepts PKCS#1 (what GitHub emits) and PKCS#8. So the decode is a few lines in `cmd/kiln` config, not a change to the adapter.

The sniff exists so that a key pasted in its natural form still works. A developer who downloads the `.pem` and pastes it — into a shell heredoc, or a Render env box that does tolerate newlines — gets a working boot rather than an "invalid key" they'd debug as a bad download. The declared, documented form is base64; the sniff is the forgiving path, not a second supported format.

Failure is at **boot**, not first sign-in: a value that is neither valid base64 nor valid PEM fails the §5 gate with a message naming which of the two it tried, so a bad paste never becomes a user-facing broken connect flow. `ParsePrivateKey`'s doc comment already promises this ordering.

Local `.env` is populated (2026-08-06) from `mac-kiln-dev`'s generated key; the `.pem` itself belongs in the password manager, not the repo tree or `~/Downloads`.

---

## 6. Migration & compatibility

Existing users hold a working OAuth token in the encrypted slot. The credential path stays polymorphic:

> **installation present → mint an installation token; else → use the stored token.**

That is the same carry-forward shape that already lets a hand-typed PAT keep working (`service.go:169-176`), so it costs one branch rather than a parallel code path. No forced re-auth, no backfill, no flag day — the property spec `11` §2 already committed to for the last merge.

The fallback is not permanent. Once the answer to §0 is known and (if chosen) existing users have migrated, the OAuth branch and the `GITHUB_OAUTH_*` env vars can be deleted in a follow-up. Until then they stay, and the OAuth App registration must remain live.

---

## 7. Prerequisite outside this repo

The App must be registered by the account/org owner. The form is filled per §4.6. It then produces three things Kiln needs:

1. **App ID** → `KILN_GITHUB_APP_ID`.
2. **Public link** (`https://github.com/apps/<slug>`) → the slug for `KILN_GITHUB_APP_SLUG`, which builds the installation URL in §3.2.
3. **A generated private key (`.pem`)** → base64 it (`base64 -i <file> | tr -d '\n'`) into `KILN_GITHUB_APP_PRIVATE_KEY` per §5.1. Generated once, downloadable once; store the `.pem` in the password manager as well as putting the base64 in Render.

Plus the App's own client id and secret (§5). Together these **replace** `GITHUB_OAUTH_CLIENT_ID` / `GITHUB_OAUTH_CLIENT_SECRET`, which stay configured only for as long as the §6 fallback does.

This cannot be done from the codebase.

---

## 8. Testing (per the `end-to-end-development` gate)

- **Unit** — JWT construction and signing; token cache hit, refresh-before-expiry, and concurrent mint; `/user/installations/{id}/repositories` paging; `setup_action=install` vs `update`; callback with `installation_id` and no `code`; the §6 fallback branch in both directions.
- **Integration** — connection state against a suspended and a revoked installation (`404`/`401` → `NeedsReconnect`, never a false "Connected"); the boot gate refusing a partial App config.
- **E2E** — the keyless stack drives `githubmock` (`backend/internal/identity/githubmock`), which must learn the installation endpoints and the mint call. `tests/tests/keyless-onboarding.spec.ts` and `brain-has-repo-access.spec.ts` both exercise the connect flow and will need updating.

## 9. Implementation order

1. `githubapi` — JWT + mint + installation-repos listing, behind the existing client shape.
2. `githubmock` — the same endpoints, so the keyless stack keeps booting throughout.
3. `identity` — installation storage, `TokenSource`, connection state, the §6 fallback.
4. `internal/repo` + `verify` — accept a token source instead of a string.
5. `cmd/kiln` — config, boot gate, wiring.
6. Schema regen, then the dashboard (onboarding step 1, Integrations Configure link, `GITHUB_ACCESS_NOTE`).
7. Registration + Render secrets (§7), then answer §0 and ship the prompt.

Steps 1–6 are mergeable behind the fallback without the App existing, since no existing credential path changes behaviour until an installation is present.
