package identity_test

// The GitHub App connect flow (design 2026-08-04) and the credential model it
// installs. Four things are under test:
//
//   - Connecting records an INSTALLATION, and the repo credential is minted from
//     it on demand rather than stored — so what lands in the database is an
//     identifier, and what git uses expires within the hour.
//   - A flow that authorized the user but installed nothing is refused as a
//     credential while still succeeding as a sign-in, so nobody is locked out of
//     the dashboard the retry lives on.
//   - An installation GitHub has rejected surfaces as needs_reconnect instead of
//     resurfacing later as a mysteriously broken agent turn.
//   - A credential Kiln did not grant — a hand-typed PAT, the deployment's
//     bootstrap token — still works, and is honestly labelled as not-ours.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/identity"
	"github.com/crabtree-michael/kiln/backend/internal/identity/githubapi"
)

const (
	// connectLogin is the allowlisted login, lower-cased as identity stores it;
	// connectGitHubLogin is GitHub's case-preserved spelling of the same account.
	connectLogin       = "crabtree-michael"
	connectGitHubLogin = "Crabtree-Michael"
	// connectInstallation is the installation the flow yields.
	connectInstallation = int64(987654)
	// connectedToken is the USER access token the flow yields — what the repo
	// picker lists with. mintedToken is the INSTALLATION token minted from
	// connectInstallation, which is what git and `gh` actually authenticate with.
	// They are deliberately different values: every assertion below turns on
	// which of the two reached which call site.
	//nolint:gosec // G101: an obviously-fake test fixture; this package has no real credentials.
	connectedToken = "gh-user-access-token"
	mintedToken    = "ghs-installation-token"
	// everythingRepo is the repo only the UNNARROWED listing returns — the one a
	// picker reading the installation must never show.
	everythingRepo = "acme/everything"
	// patToken stands in for a credential Kiln never granted: typed into
	// PUT /api/settings, or seeded from the deployment's GITHUB_AUTH_TOKEN.
	//nolint:gosec // G101: as above — a fixture, not a credential.
	patToken = "gh-hand-typed-pat"
)

// connectService builds a service whose GitHub double is primed for the connect
// flow, allowlisting the account it will find-or-create.
func connectService(t *testing.T, gh *fakeGitHub) *identity.Service {
	t.Helper()
	return newTestService(t, newFakeStore(), gh, []string{connectLogin})
}

// connectGitHub is a GitHub double primed to complete the flow: it hands back
// the user token, the profile, and a freshly-minted installation credential with
// an hour of life, exactly as GitHub does.
func connectGitHub() *fakeGitHub {
	return &fakeGitHub{
		token:  connectedToken,
		user:   githubapi.GitHubUser{ID: 42, Login: connectGitHubLogin},
		minted: githubapi.InstallationToken{Token: mintedToken, ExpiresAt: time.Now().Add(time.Hour)},
	}
}

// settingsOf reads the account view's settings for a user.
func settingsOf(t *testing.T, svc *identity.Service, userID string) identity.MeSettings {
	t.Helper()
	me, err := svc.Me(context.Background(), userID)
	if err != nil {
		t.Fatalf("Me: %v", err)
	}
	return me.Settings
}

// repoTokenFor drives the credential the runtime would actually use for a
// project: create one, resolve its RuntimeConfig, and call the token source the
// way `gh` does. This is the assertion that matters most in this file — it is
// the whole path from a stored row to a live credential.
func repoTokenFor(t *testing.T, svc *identity.Service, userID string) (string, error) {
	t.Helper()
	if _, err := svc.UpsertProject(context.Background(), userID, identity.ProjectUpdate{
		Name: "p", RepoURL: "https://github.com/acme/demo", WorkerCount: 1,
	}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}
	proj, err := svc.ProjectFor(context.Background(), userID)
	if err != nil {
		t.Fatalf("ProjectFor: %v", err)
	}
	rc, err := svc.RuntimeConfig(context.Background(), proj.ID)
	if err != nil {
		t.Fatalf("RuntimeConfig: %v", err)
	}
	//nolint:wrapcheck // a test helper: the caller asserts on this error as-is.
	return rc.GitHubToken(context.Background())
}

func TestCompleteConnectRecordsInstallationAndMintsCredential(t *testing.T) {
	gh := connectGitHub()
	svc := connectService(t, gh)

	user, err := svc.CompleteConnect(context.Background(), "code-1", connectInstallation)
	if err != nil {
		t.Fatalf("CompleteConnect: %v", err)
	}

	set := settingsOf(t, svc, user.ID)
	if set.GitHub.Status != identity.GitHubConnected {
		t.Errorf("status = %q, want %q", set.GitHub.Status, identity.GitHubConnected)
	}
	if set.GitHub.InstallationID != connectInstallation {
		t.Errorf("installation = %d, want %d", set.GitHub.InstallationID, connectInstallation)
	}
	if set.GitHub.Login != connectLogin {
		t.Errorf("login = %q, want %q", set.GitHub.Login, connectLogin)
	}
	// The card links out to GitHub's own chooser — that link is the only way a
	// user can change which repositories Kiln may touch.
	if set.GitHub.ConfigureURL == "" {
		t.Error("configure URL must be set so the card can offer 'Configure on GitHub'")
	}
	// The USER token is stored (the picker needs it); the repo credential is not
	// stored at all.
	if !set.GitHubToken.Set {
		t.Error("the user access token must be stored — the repo picker lists with it")
	}

	// What git and `gh` get is the MINTED token, never the stored user token.
	// This is the entire point of the migration: the credential that touches
	// repositories is short-lived and was never written down.
	token, err := repoTokenFor(t, svc, user.ID)
	if err != nil {
		t.Fatalf("resolve repo token: %v", err)
	}
	if token != mintedToken {
		t.Errorf("repo credential = %q, want the minted installation token %q", token, mintedToken)
	}
	if got := gh.gotMintedInstallationIDs; len(got) != 1 || got[0] != connectInstallation {
		t.Errorf("minted against %v, want exactly [%d]", got, connectInstallation)
	}
}

// A user who cleared GitHub's sign-in screen and then backed out of the install
// itself: the account authenticated, so the caller signs them in, but no
// repository access was granted and none must be recorded.
func TestCompleteConnectWithoutInstallationSignsInButStoresNothing(t *testing.T) {
	gh := connectGitHub()
	svc := connectService(t, gh)

	denied, err := svc.CompleteConnect(context.Background(), "code-1", 0)
	if !errors.Is(err, identity.ErrInstallationRequired) {
		t.Fatalf("err = %v, want ErrInstallationRequired", err)
	}
	if denied.ID == "" || denied.GitHubLogin != connectLogin {
		t.Errorf("user = %+v, want the authenticated %s so the caller can still sign them in",
			denied, connectLogin)
	}

	set := settingsOf(t, svc, denied.ID)
	if set.GitHub.Status != identity.GitHubDisconnected {
		t.Errorf("status = %q, want %q", set.GitHub.Status, identity.GitHubDisconnected)
	}
	if set.GitHub.InstallationID != 0 {
		t.Errorf("installation = %d, want 0", set.GitHub.InstallationID)
	}
	if set.GitHubToken.Set {
		t.Error("no credential may be stored when nothing was installed")
	}
}

// Re-running the flow is how a user changes their repository selection: they
// return from GitHub's Configure screen with setup_action=update. The new
// installation replaces the old one, and — the subtle part — the cached token
// for it is dropped, so the next git call cannot keep using a credential minted
// against the selection the user just changed.
func TestReconnectReplacesInstallationAndDropsTheCachedToken(t *testing.T) {
	gh := connectGitHub()
	svc := connectService(t, gh)

	user, err := svc.CompleteConnect(context.Background(), "code-1", connectInstallation)
	if err != nil {
		t.Fatalf("CompleteConnect: %v", err)
	}
	if _, err := repoTokenFor(t, svc, user.ID); err != nil {
		t.Fatalf("resolve repo token: %v", err)
	}
	if got := gh.mintCallCount(); got != 1 {
		t.Fatalf("mint calls = %d, want 1", got)
	}

	const secondInstallation = int64(111222)
	if _, err := svc.CompleteConnect(context.Background(), "code-2", secondInstallation); err != nil {
		t.Fatalf("second CompleteConnect: %v", err)
	}
	if got := settingsOf(t, svc, user.ID).GitHub.InstallationID; got != secondInstallation {
		t.Errorf("installation = %d, want the new %d", got, secondInstallation)
	}
	if _, err := repoTokenFor(t, svc, user.ID); err != nil {
		t.Fatalf("resolve repo token after reconnect: %v", err)
	}
	if got := gh.mintCallCount(); got != 2 {
		t.Errorf("mint calls = %d, want 2 — a reconnect must not reuse the old selection's token", got)
	}
}

// Installing from GitHub's own Apps page: the browser arrives with an
// installation and no code, because it never passed through Kiln's authorize
// step. There is nothing to exchange, so the stored user token must survive
// untouched while the installation is attached.
func TestAttachInstallationKeepsTheStoredUserToken(t *testing.T) {
	gh := connectGitHub()
	svc := connectService(t, gh)
	user, err := svc.CompleteConnect(context.Background(), "code-1", connectInstallation)
	if err != nil {
		t.Fatalf("CompleteConnect: %v", err)
	}
	before := settingsOf(t, svc, user.ID).GitHubToken

	const otherInstallation = int64(555)
	if err := svc.AttachInstallation(context.Background(), user.ID, otherInstallation); err != nil {
		t.Fatalf("AttachInstallation: %v", err)
	}

	set := settingsOf(t, svc, user.ID)
	if set.GitHub.InstallationID != otherInstallation {
		t.Errorf("installation = %d, want %d", set.GitHub.InstallationID, otherInstallation)
	}
	if set.GitHubToken != before {
		t.Errorf("user token = %+v, want it untouched at %+v — there was no code to exchange",
			set.GitHubToken, before)
	}
	if set.GitHub.Login != connectLogin {
		t.Errorf("login = %q, want the recorded %q", set.GitHub.Login, connectLogin)
	}
}

func TestAttachInstallationRejectsAMissingInstallation(t *testing.T) {
	svc := connectService(t, connectGitHub())
	user, err := svc.EnsureUser(context.Background(), connectLogin)
	if err != nil {
		t.Fatalf("EnsureUser: %v", err)
	}
	if err := svc.AttachInstallation(context.Background(), user.ID, 0); !errors.Is(
		err, identity.ErrInstallationRequired,
	) {
		t.Fatalf("err = %v, want ErrInstallationRequired", err)
	}
}

// A credential Kiln did not grant keeps working: it is used verbatim (there is
// nothing to mint against) and reported as stored-but-not-ours rather than as
// broken. This is the PUT /api/settings field and the bootstrap GITHUB_AUTH_TOKEN.
func TestHandTypedTokenWorksAndReadsAsUnknown(t *testing.T) {
	gh := connectGitHub()
	svc := connectService(t, gh)
	user, err := svc.EnsureUser(context.Background(), connectLogin)
	if err != nil {
		t.Fatalf("EnsureUser: %v", err)
	}
	if serr := svc.UpdateSettings(context.Background(), user.ID, identity.SettingsUpdate{
		GitHubToken: patToken,
	}); serr != nil {
		t.Fatalf("UpdateSettings: %v", serr)
	}

	set := settingsOf(t, svc, user.ID)
	if set.GitHub.Status != identity.GitHubUnknownScopes {
		t.Errorf("status = %q, want %q (stored, no installation behind it)",
			set.GitHub.Status, identity.GitHubUnknownScopes)
	}
	if set.GitHub.ConfigureURL != "" {
		t.Error("there is no installation to configure, so the link must be absent")
	}

	token, err := repoTokenFor(t, svc, user.ID)
	if err != nil {
		t.Fatalf("resolve repo token: %v", err)
	}
	if token != patToken {
		t.Errorf("repo credential = %q, want the stored token verbatim", token)
	}
	if got := gh.mintCallCount(); got != 0 {
		t.Errorf("mint calls = %d, want 0 — there is no installation to mint against", got)
	}
}

// Writing a raw token over an App connection must drop the installation with
// it. Otherwise the card would keep claiming an installation while the runtime
// authenticated with something else entirely.
func TestUpdateSettingsClearsTheInstallation(t *testing.T) {
	gh := connectGitHub()
	svc := connectService(t, gh)
	user, err := svc.CompleteConnect(context.Background(), "code-1", connectInstallation)
	if err != nil {
		t.Fatalf("CompleteConnect: %v", err)
	}

	if serr := svc.UpdateSettings(context.Background(), user.ID, identity.SettingsUpdate{
		GitHubToken: patToken,
	}); serr != nil {
		t.Fatalf("UpdateSettings: %v", serr)
	}

	set := settingsOf(t, svc, user.ID)
	if set.GitHub.InstallationID != 0 {
		t.Errorf("installation = %d, want 0 — this token has nothing to do with it",
			set.GitHub.InstallationID)
	}
	if set.GitHub.Status != identity.GitHubUnknownScopes {
		t.Errorf("status = %q, want %q", set.GitHub.Status, identity.GitHubUnknownScopes)
	}
	if set.GitHub.Login != "" {
		t.Errorf("login = %q, want empty — it belonged to the replaced connection", set.GitHub.Login)
	}
}

// An installation GitHub rejects (uninstalled, suspended, access withdrawn)
// must become a visible "reconnect" rather than an invisible failure. The mint's
// 404 is recorded against every user on that installation, so the very next read
// of the card says so.
func TestRejectedMintFlipsTheCardToNeedsReconnect(t *testing.T) {
	gh := connectGitHub()
	svc := connectService(t, gh)
	user, err := svc.CompleteConnect(context.Background(), "code-1", connectInstallation)
	if err != nil {
		t.Fatalf("CompleteConnect: %v", err)
	}
	if got := settingsOf(t, svc, user.ID).GitHub.Status; got != identity.GitHubConnected {
		t.Fatalf("status = %q, want connected before the installation dies", got)
	}

	gh.mintErr = githubapi.ErrInstallationUnavailable
	if _, err := repoTokenFor(t, svc, user.ID); err == nil {
		t.Fatal("resolving the repo credential must fail once the installation is gone")
	}

	set := settingsOf(t, svc, user.ID)
	if set.GitHub.Status != identity.GitHubNeedsReconnect {
		t.Errorf("status = %q, want %q", set.GitHub.Status, identity.GitHubNeedsReconnect)
	}
	// The installation id survives the revocation: it is what the card renders
	// and what a reconnect replaces, not something to forget on the first 404.
	if set.GitHub.InstallationID != connectInstallation {
		t.Errorf("installation = %d, want it retained", set.GitHub.InstallationID)
	}

	// Reconnecting clears the mark — the user has just answered whatever killed it.
	gh.mintErr = nil
	if _, err := svc.CompleteConnect(context.Background(), "code-2", connectInstallation); err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	if got := settingsOf(t, svc, user.ID).GitHub.Status; got != identity.GitHubConnected {
		t.Errorf("status = %q, want connected after reconnecting", got)
	}
}

// A transport failure is NOT a revocation. A network blip must never leave a
// perfectly good installation labelled as needing a reconnect — that would send
// users through GitHub's install screen to fix Kiln's own connectivity.
func TestTransientMintFailureDoesNotRevoke(t *testing.T) {
	gh := connectGitHub()
	svc := connectService(t, gh)
	user, err := svc.CompleteConnect(context.Background(), "code-1", connectInstallation)
	if err != nil {
		t.Fatalf("CompleteConnect: %v", err)
	}

	gh.mintErr = githubapi.ErrMintToken // the transport-failure base, not the gone sentinel
	if _, err := repoTokenFor(t, svc, user.ID); err == nil {
		t.Fatal("a failed mint must surface as an error")
	}

	if got := settingsOf(t, svc, user.ID).GitHub.Status; got != identity.GitHubConnected {
		t.Errorf("status = %q, want it still connected — GitHub never said the installation was gone", got)
	}
}

// Verify is the user-facing action that discovers a dead installation: the repo
// check mints, fails, and the card is marked in the same run that reports the
// repo unreachable.
func TestVerifySurfacesADeadInstallation(t *testing.T) {
	gh := connectGitHub()
	gh.mintErr = githubapi.ErrInstallationUnavailable
	svc := connectService(t, gh)
	svc.SetVerifier(&fakeVerifier{})
	user, err := svc.CompleteConnect(context.Background(), "code-1", connectInstallation)
	if err != nil {
		t.Fatalf("CompleteConnect: %v", err)
	}
	if _, err := svc.UpsertProject(context.Background(), user.ID, identity.ProjectUpdate{
		Name: "p", RepoURL: "https://github.com/acme/demo", WorkerCount: 1,
	}); err != nil {
		t.Fatalf("UpsertProject: %v", err)
	}

	if _, err := svc.Verify(context.Background(), user.ID); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	if got := settingsOf(t, svc, user.ID).GitHub.Status; got != identity.GitHubNeedsReconnect {
		t.Errorf("status = %q, want %q after a verify that could not mint",
			got, identity.GitHubNeedsReconnect)
	}
}

// The repo picker is where the user sees what the App bought them: with an
// installation it lists only the repositories they ticked on GitHub's chooser,
// not everything the account can reach.
func TestListGitHubReposReadsTheInstallationListing(t *testing.T) {
	gh := connectGitHub()
	gh.repos = []githubapi.Repo{{FullName: everythingRepo, HTMLURL: "https://github.com/" + everythingRepo}}
	gh.installationRepos = []githubapi.Repo{{FullName: "acme/picked", HTMLURL: "https://github.com/acme/picked"}}
	svc := connectService(t, gh)
	user, err := svc.CompleteConnect(context.Background(), "code-1", connectInstallation)
	if err != nil {
		t.Fatalf("CompleteConnect: %v", err)
	}

	repos, err := svc.ListGitHubRepos(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("ListGitHubRepos: %v", err)
	}
	if len(repos) != 1 || repos[0].FullName != "acme/picked" {
		t.Errorf("repos = %+v, want only the installation's selection", repos)
	}
	if gh.gotInstallationReposID != connectInstallation {
		t.Errorf("listed installation %d, want %d", gh.gotInstallationReposID, connectInstallation)
	}
	// The listing is USER-scoped: it must present the user token, not a minted
	// one, so an org installation never offers repos this member cannot see.
	if gh.gotReposToken != connectedToken {
		t.Errorf("listed with %q, want the user access token", gh.gotReposToken)
	}
}

// Without an installation there is nothing to narrow by, so the picker falls
// back to whatever the raw token can reach.
func TestListGitHubReposFallsBackWithoutAnInstallation(t *testing.T) {
	gh := connectGitHub()
	gh.repos = []githubapi.Repo{{FullName: everythingRepo, HTMLURL: "https://github.com/" + everythingRepo}}
	svc := connectService(t, gh)
	user, err := svc.EnsureUser(context.Background(), connectLogin)
	if err != nil {
		t.Fatalf("EnsureUser: %v", err)
	}
	if serr := svc.UpdateSettings(context.Background(), user.ID, identity.SettingsUpdate{
		GitHubToken: patToken,
	}); serr != nil {
		t.Fatalf("UpdateSettings: %v", serr)
	}

	repos, err := svc.ListGitHubRepos(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("ListGitHubRepos: %v", err)
	}
	if len(repos) != 1 || repos[0].FullName != everythingRepo {
		t.Errorf("repos = %+v, want the token's own reach", repos)
	}
	if gh.gotInstallationReposID != 0 {
		t.Error("no installation exists, so the installation listing must not be called")
	}
}

// Amika/Devin keys are untouched by all of this: they live in their own slots
// and a stored key keeps reading as configured, so nobody re-enters a working
// key because of the migration.
func TestExistingProviderKeysSurviveUnchanged(t *testing.T) {
	gh := connectGitHub()
	svc := connectService(t, gh)
	user, err := svc.EnsureUser(context.Background(), connectLogin)
	if err != nil {
		t.Fatalf("EnsureUser: %v", err)
	}
	if err := svc.UpdateSettings(context.Background(), user.ID, identity.SettingsUpdate{
		AmikaKey: "sk-amika-existing", DevinKey: "cog-devin-existing",
	}); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}

	// Connecting GitHub afterwards must not disturb them.
	if _, err := svc.CompleteConnect(context.Background(), "code-1", connectInstallation); err != nil {
		t.Fatalf("CompleteConnect: %v", err)
	}

	set := settingsOf(t, svc, user.ID)
	if !set.AmikaKey.Set || !set.DevinKey.Set {
		t.Fatalf("amika/devin keys must survive: amika=%v devin=%v", set.AmikaKey.Set, set.DevinKey.Set)
	}
	if set.AmikaKey.Tail != "ting" || set.DevinKey.Tail != "ting" {
		t.Errorf("tails = %q/%q, want the stored keys' tails", set.AmikaKey.Tail, set.DevinKey.Tail)
	}
}

// ConnectURL is the one entry point every affordance leads to, and it must point
// at the install page — the screen that renders the repository chooser. Pointing
// it anywhere else is how the whole feature silently stops existing.
func TestConnectURLTargetsTheInstallPage(t *testing.T) {
	svc := connectService(t, connectGitHub())
	got := svc.ConnectURL("state-nonce")
	if got != "https://github.example/apps/kiln/installations/new?state=state-nonce" {
		t.Errorf("ConnectURL = %q, want the App's install page carrying the state nonce", got)
	}
}
