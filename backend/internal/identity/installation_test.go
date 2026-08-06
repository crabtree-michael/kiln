package identity_test

// The installation-token cache (design 2026-08-04 §3.3). Minting is a network
// round trip on the hot path of every git and `gh` invocation, so what these
// tests pin down is when it happens and when it must not: cached inside the
// token's life, refreshed before expiry rather than after, collapsed to one
// call under concurrency, and reported — not silently swallowed — when the
// installation is gone.

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/identity"
	"github.com/crabtree-michael/kiln/backend/internal/identity/githubapi"
)

// countingMinter hands out a distinct token per call so a test can tell a
// cached credential from a freshly minted one by value alone, and can be
// switched to failing mid-test.
type countingMinter struct {
	mu    sync.Mutex
	calls int
	ttl   time.Duration
	err   error
	// block, when non-nil, holds every mint until it is closed — the lever the
	// singleflight test uses to guarantee overlapping callers.
	block chan struct{}
}

func (m *countingMinter) MintInstallationToken(
	_ context.Context, _ int64,
) (githubapi.InstallationToken, error) {
	m.mu.Lock()
	block := m.block
	m.mu.Unlock()
	if block != nil {
		<-block
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return githubapi.InstallationToken{}, m.err
	}
	m.calls++
	ttl := m.ttl
	if ttl == 0 {
		ttl = time.Hour
	}
	return githubapi.InstallationToken{
		Token:     tokenName(m.calls),
		ExpiresAt: time.Now().Add(ttl),
	}, nil
}

func (m *countingMinter) callCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

// tokenName is the nth minted token's value.
func tokenName(n int) string {
	return "ghs-token-" + strconv.Itoa(n)
}

func TestInstallationTokensCachesWithinTheTokensLife(t *testing.T) {
	m := &countingMinter{ttl: time.Hour}
	tokens := identity.NewInstallationTokens(m)

	first, err := tokens.Token(context.Background(), 1)
	if err != nil {
		t.Fatalf("first Token: %v", err)
	}
	second, err := tokens.Token(context.Background(), 1)
	if err != nil {
		t.Fatalf("second Token: %v", err)
	}

	if first != second {
		t.Errorf("tokens = %q then %q, want the cached one both times", first, second)
	}
	if got := m.callCount(); got != 1 {
		t.Errorf("mint calls = %d, want 1 — a cached token must not cost a round trip", got)
	}
}

// A token handed out at the last second dies mid-clone. The cache therefore
// treats anything inside the refresh margin as spent, which is the difference
// between a working `gh` call and an intermittent 401 nobody can reproduce.
func TestInstallationTokensRefreshesBeforeExpiry(t *testing.T) {
	// A minute of life is well inside the five-minute margin, so the "cached"
	// token is already considered spent on the very next call.
	m := &countingMinter{ttl: time.Minute}
	tokens := identity.NewInstallationTokens(m)

	if _, err := tokens.Token(context.Background(), 1); err != nil {
		t.Fatalf("first Token: %v", err)
	}
	if _, err := tokens.Token(context.Background(), 1); err != nil {
		t.Fatalf("second Token: %v", err)
	}

	if got := m.callCount(); got != 2 {
		t.Errorf("mint calls = %d, want 2 — a token about to expire must be replaced, not reused", got)
	}
}

// A fleet of workers waking together must cost ONE mint, not one each. The
// block channel guarantees they genuinely overlap: every goroutine is parked
// inside the minter before any of them is allowed to finish.
func TestInstallationTokensCollapsesConcurrentMints(t *testing.T) {
	m := &countingMinter{ttl: time.Hour, block: make(chan struct{})}
	tokens := identity.NewInstallationTokens(m)

	const callers = 8
	var wg sync.WaitGroup
	got := make([]string, callers)
	errs := make([]error, callers)
	for i := range callers {
		wg.Go(func() {
			got[i], errs[i] = tokens.Token(context.Background(), 1)
		})
	}
	// Give every goroutine a chance to reach the minter (or queue on the
	// singleflight) before releasing them.
	time.Sleep(20 * time.Millisecond)
	close(m.block)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
		if got[i] != got[0] {
			t.Errorf("caller %d got %q, want the same token as caller 0 (%q)", i, got[i], got[0])
		}
	}
	if calls := m.callCount(); calls != 1 {
		t.Errorf("mint calls = %d, want 1 — concurrent callers must share one mint", calls)
	}
}

// Different installations are independent: one slow or dead installation must
// not serialize or poison another's credential.
func TestInstallationTokensKeyByInstallation(t *testing.T) {
	m := &countingMinter{ttl: time.Hour}
	tokens := identity.NewInstallationTokens(m)

	first, err := tokens.Token(context.Background(), 1)
	if err != nil {
		t.Fatalf("Token(1): %v", err)
	}
	second, err := tokens.Token(context.Background(), 2)
	if err != nil {
		t.Fatalf("Token(2): %v", err)
	}

	if first == second {
		t.Error("two installations shared a token; each must mint its own")
	}
	if got := m.callCount(); got != 2 {
		t.Errorf("mint calls = %d, want 2", got)
	}
}

// Forget is what a reconnect calls: the user may have changed which
// repositories the installation covers, and a token minted against the old
// selection would keep reaching repos they just removed.
func TestInstallationTokensForgetDropsTheCachedToken(t *testing.T) {
	m := &countingMinter{ttl: time.Hour}
	tokens := identity.NewInstallationTokens(m)

	first, err := tokens.Token(context.Background(), 1)
	if err != nil {
		t.Fatalf("first Token: %v", err)
	}
	tokens.Forget(1)
	second, err := tokens.Token(context.Background(), 1)
	if err != nil {
		t.Fatalf("second Token: %v", err)
	}

	if first == second {
		t.Error("Forget did not drop the cached token")
	}
	if got := m.callCount(); got != 2 {
		t.Errorf("mint calls = %d, want 2", got)
	}
}

// A gone installation is reported through the hook, which is how a runtime
// credential failure becomes a visible "reconnect" on the dashboard instead of
// a mystery. A transport failure must NOT fire it — a network blip is not a
// revoked grant.
func TestInstallationTokensReportsOnlyRealUnavailability(t *testing.T) {
	cases := []struct {
		name       string
		mintErr    error
		wantNotify bool
	}{
		{name: "installation gone", mintErr: githubapi.ErrInstallationUnavailable, wantNotify: true},
		{name: "transport failure", mintErr: githubapi.ErrMintToken},
		{name: "no app credentials", mintErr: githubapi.ErrNoAppCredentials},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := &countingMinter{err: tc.mintErr}
			tokens := identity.NewInstallationTokens(m)
			var notified []int64
			tokens.OnUnavailable(func(_ context.Context, id int64) {
				notified = append(notified, id)
			})

			if _, err := tokens.Token(context.Background(), 77); !errors.Is(err, tc.mintErr) {
				t.Fatalf("err = %v, want it to wrap %v", err, tc.mintErr)
			}

			switch {
			case tc.wantNotify && (len(notified) != 1 || notified[0] != 77):
				t.Errorf("notified %v, want exactly [77]", notified)
			case !tc.wantNotify && len(notified) != 0:
				t.Errorf("notified %v, want nothing — this is not a revoked installation", notified)
			}
		})
	}
}

// A transient failure must not throw away a token that still has minutes of
// life on it: the next call retries, and in the meantime a working credential
// is better than none.
func TestInstallationTokensKeepsTheCachedTokenThroughAFailedRefresh(t *testing.T) {
	m := &countingMinter{ttl: time.Hour}
	tokens := identity.NewInstallationTokens(m)

	first, err := tokens.Token(context.Background(), 1)
	if err != nil {
		t.Fatalf("first Token: %v", err)
	}

	m.mu.Lock()
	m.err = githubapi.ErrMintToken
	m.mu.Unlock()
	// Still inside the token's life, so this is served from cache and never
	// reaches the failing minter at all.
	second, err := tokens.Token(context.Background(), 1)
	if err != nil {
		t.Fatalf("second Token: %v", err)
	}
	if second != first {
		t.Errorf("token = %q, want the still-valid cached %q", second, first)
	}
}

// StaticTokenSource is the no-installation path: nothing to mint, nothing to
// refresh, and an unconfigured credential is an ordinary empty answer rather
// than an error the caller has to special-case.
func TestStaticTokenSource(t *testing.T) {
	got, err := identity.StaticTokenSource("tok")(context.Background())
	if err != nil || got != "tok" {
		t.Errorf("got (%q, %v), want (\"tok\", nil)", got, err)
	}
	got, err = identity.StaticTokenSource("")(context.Background())
	if err != nil || got != "" {
		t.Errorf("got (%q, %v), want (\"\", nil) — unconfigured is not a failure", got, err)
	}
}
