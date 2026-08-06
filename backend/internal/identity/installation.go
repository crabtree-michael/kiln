package identity

// The GitHub App credential path: how a stored installation id becomes a live
// token that git and `gh` can authenticate with (design 2026-08-04 §3.3).
//
// The structural difference from the OAuth App this replaced: there is no
// long-lived token to store. Kiln holds the App's private key, and mints an
// installation access token — scoped to the repositories the user picked, valid
// for about an hour — whenever one is needed. So the credential stops being a
// VALUE that call sites can be handed at wiring time and becomes a FUNCTION they
// call at use time. That is what TokenSource is.
//
// Minting is a network round trip, so it must not happen per git invocation:
// InstallationTokens caches per installation and refreshes shortly before
// expiry, and a burst of concurrent callers for the same installation collapses
// onto ONE in-flight mint rather than stampeding GitHub.

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/identity/githubapi"
)

// refreshMargin is how long before GitHub's stated expiry a cached token is
// considered spent. A token handed out at the last second would die mid-clone;
// five minutes is comfortably longer than any single git/`gh` invocation the
// brain makes, so a token this cache returns is good for the whole call.
const refreshMargin = 5 * time.Minute

// TokenSource resolves the GitHub credential for one user AT CALL TIME. Every
// repo-touching call site takes one of these instead of a string, because an
// installation token expires within the hour and a value captured at wiring
// time would be dead long before the process that captured it.
//
// The returned string is a LIVE SECRET: never log it, never persist it, never
// put it in a URL that gets logged. It is also short-lived — resolve it per
// invocation rather than hoisting it into a longer-lived variable.
type TokenSource func(ctx context.Context) (string, error)

// StaticTokenSource adapts a fixed credential to the TokenSource shape — the
// hand-typed PAT and bootstrap GITHUB_AUTH_TOKEN path, where there is nothing to
// mint and nothing to refresh. An empty token yields an empty string rather than
// an error: an unconfigured credential is a normal state (the caller renders
// "not connected"), not a failure.
func StaticTokenSource(token string) TokenSource {
	return func(context.Context) (string, error) { return token, nil }
}

// Minter is the port onto the App's mint call — satisfied directly by
// *githubapi.Client (the consumer declares the interface, 02 §2). Split out from
// the wider GitHub port so InstallationTokens depends only on the one call it
// makes, and so a test can drive the cache with a counting stub.
//
// Like the GitHub port, it speaks githubapi's own result type rather than a
// re-declared twin: the adapter's ExpiresAt IS GitHub's answer, and copying it
// through a second struct would only add a place for the two to disagree.
type Minter interface {
	MintInstallationToken(ctx context.Context, installationID int64) (githubapi.InstallationToken, error)
}

// entry is one installation's cached token plus the singleflight latch for it.
// The mutex guards a mint in progress, NOT the map: holding the map's lock
// across a network call would serialize every installation behind the slowest
// one.
type entry struct {
	mu    sync.Mutex
	token string
	// expires is GitHub's stated expiry; the token is treated as spent
	// refreshMargin before it.
	expires time.Time
}

// InstallationTokens mints and caches installation access tokens, one entry per
// installation id. Safe for concurrent use.
//
// It deliberately holds no user or project vocabulary: it maps an installation
// id to a live token and nothing more. Which user owns which installation is the
// Service's business (SourceFor).
type InstallationTokens struct {
	minter Minter
	now    func() time.Time

	mu      sync.Mutex
	entries map[int64]*entry
	// onUnavailable is called with the installation id when GitHub reports the
	// installation gone, so the Service can record the revocation. Optional
	// (nil-safe) — the cache works without anyone listening.
	onUnavailable func(ctx context.Context, installationID int64)
}

// NewInstallationTokens builds the cache over a minter.
func NewInstallationTokens(minter Minter) *InstallationTokens {
	return &InstallationTokens{minter: minter, now: time.Now, entries: map[int64]*entry{}}
}

// OnUnavailable registers the callback fired when a mint reports the
// installation gone. Setter, not a constructor arg, because the Service that
// wants to hear about it is built AFTER the cache it is built over.
func (t *InstallationTokens) OnUnavailable(f func(ctx context.Context, installationID int64)) {
	t.onUnavailable = f
}

// Token returns a live token for installationID, minting one only when there is
// no cached token or the cached one is within refreshMargin of expiry.
//
// Concurrent callers for the SAME installation collapse onto one mint: the
// second caller blocks on that installation's mutex and then finds the fresh
// token already cached, so a fleet of workers waking together costs one round
// trip rather than one each. Callers for DIFFERENT installations never block
// each other.
func (t *InstallationTokens) Token(ctx context.Context, installationID int64) (string, error) {
	e := t.entryFor(installationID)

	e.mu.Lock()
	defer e.mu.Unlock()
	// Re-check under the lock: whoever we queued behind has probably just
	// refreshed it, and this is the whole point of the singleflight.
	if e.token != "" && t.now().Before(e.expires.Add(-refreshMargin)) {
		return e.token, nil
	}

	minted, err := t.minter.MintInstallationToken(ctx, installationID)
	if err != nil {
		if errors.Is(err, githubapi.ErrInstallationUnavailable) && t.onUnavailable != nil {
			t.onUnavailable(ctx, installationID)
		}
		// Leave any stale cached token in place rather than clearing it: a
		// transient mint failure should not throw away a credential that may
		// still have minutes of life left, and the next call retries anyway.
		return "", fmt.Errorf("identity: installation token: %w", err)
	}
	e.token, e.expires = minted.Token, minted.ExpiresAt
	return e.token, nil
}

// Forget drops an installation's cached token, so the next Token call mints
// fresh. Called when the user re-runs the install flow: the repository selection
// may have changed, and a token minted against the old selection would keep
// working against repos the user has just removed.
func (t *InstallationTokens) Forget(installationID int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.entries, installationID)
}

// entryFor returns installationID's entry, creating it on first use. Only the
// map is guarded here; the mint itself happens under the entry's own lock.
func (t *InstallationTokens) entryFor(installationID int64) *entry {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.entries[installationID]
	if !ok {
		e = &entry{}
		t.entries[installationID] = e
	}
	return e
}
