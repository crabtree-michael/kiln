package identity_test

// The repo-scoped "Connect GitHub" grant (integrations redesign) and the
// carry-forward guarantee that comes with it. Two things are under test:
//
//   - Connecting stores a REAL repo credential — in the same encrypted slot the
//     removed manual token field wrote to — and refuses a grant that came back
//     without `repo`, so the dashboard card can never read "Connected" over a
//     token that cannot clone or push.
//   - A token already stored by the old manual field is never stranded: it is
//     read as connected, and only a scope list GitHub positively reports
//     WITHOUT `repo` downgrades it to needs-reconnect.

import (
	"context"
	"errors"
	"testing"

	"github.com/crabtree-michael/kiln/backend/internal/identity"
	"github.com/crabtree-michael/kiln/backend/internal/identity/githubapi"
)

const (
	// connectLogin is the allowlisted login, lower-cased as identity stores it;
	// connectGitHubLogin is GitHub's case-preserved spelling of the same account.
	connectLogin       = "crabtree-michael"
	connectGitHubLogin = "Crabtree-Michael"
	// scopeRepo is GitHub's full read/write repository scope.
	scopeRepo = "repo"
	// connectedToken is what the OAuth connect grant yields; legacyToken is what
	// the removed manual field had already stored.
	connectedToken = "gh-connected-token"
	legacyToken    = "gh-legacy-manual-token"
)

// errProbeFailed stands in for a scope probe that could not reach GitHub
// (static, per the err113 rule against dynamic errors).
var errProbeFailed = errors.New("identity_test: scope probe failed")

// connectService builds a service whose GitHub double is primed for the
// connect dance, allowlisting the account it will find-or-create.
func connectService(t *testing.T, gh *fakeGitHub) *identity.Service {
	t.Helper()
	return identity.NewService(newFakeStore(), mustCipher(t), gh, []string{connectLogin})
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

func TestCompleteConnectStoresRepoCredential(t *testing.T) {
	gh := &fakeGitHub{
		token: connectedToken,
		scope: "repo,gist",
		user:  githubapi.GitHubUser{ID: 42, Login: connectGitHubLogin},
	}
	svc := connectService(t, gh)

	user, err := svc.CompleteConnect(context.Background(), "code-1")
	if err != nil {
		t.Fatalf("CompleteConnect: %v", err)
	}

	set := settingsOf(t, svc, user.ID)
	// The credential landed in the SAME slot the manual field used — that is
	// what makes the connect flow a drop-in replacement for it.
	if !set.GitHubToken.Set {
		t.Fatal("github token not stored — connect must write the repo credential")
	}
	if set.GitHub.Status != identity.GitHubConnected {
		t.Errorf("status = %q, want %q", set.GitHub.Status, identity.GitHubConnected)
	}
	if set.GitHub.Login != connectLogin {
		t.Errorf("login = %q, want %q", set.GitHub.Login, connectLogin)
	}
	if len(set.GitHub.Scopes) != 2 || set.GitHub.Scopes[0] != scopeRepo {
		t.Errorf("scopes = %v, want [repo gist]", set.GitHub.Scopes)
	}

	// The stored token is the one GitHub issued: RuntimeConfig is what the
	// brain/sandbox actually authenticate with, so it has to carry it.
	if _, perr := svc.UpsertProject(context.Background(), user.ID, identity.ProjectUpdate{
		Name: "p", RepoURL: "https://github.com/acme/demo", WorkerCount: 1,
	}); perr != nil {
		t.Fatalf("UpsertProject: %v", perr)
	}
	proj, err := svc.ProjectFor(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("ProjectFor: %v", err)
	}
	rc, err := svc.RuntimeConfig(context.Background(), proj.ID)
	if err != nil {
		t.Fatalf("RuntimeConfig: %v", err)
	}
	if rc.GitHubAuthToken != connectedToken {
		t.Errorf("RuntimeConfig token = %q, want the connected token", rc.GitHubAuthToken)
	}
}

// A grant GitHub narrowed (the user unticked an org, or the app isn't approved
// there) must not be stored — otherwise the card goes green over a token that
// fails every clone and push. The user still comes back alongside the error:
// now that this is also the sign-in path, the caller signs them in anyway, so
// they land on the retry inside the app rather than back outside it.
func TestCompleteConnectRejectsGrantWithoutRepoScope(t *testing.T) {
	for _, granted := range []string{"", "public_repo", "gist,read:user"} {
		t.Run("scope="+granted, func(t *testing.T) {
			gh := &fakeGitHub{
				token: "gho_narrow",
				scope: granted,
				user:  githubapi.GitHubUser{ID: 42, Login: connectGitHubLogin},
			}
			svc := connectService(t, gh)

			denied, err := svc.CompleteConnect(context.Background(), "code-1")
			if !errors.Is(err, identity.ErrRepoScopeNotGranted) {
				t.Fatalf("err = %v, want ErrRepoScopeNotGranted", err)
			}
			if denied.ID == "" || denied.GitHubLogin != connectLogin {
				t.Errorf("user = %+v, want the authenticated %s so the caller can still sign them in",
					denied, connectLogin)
			}

			// The user was still created (the allowlist passed), but no
			// credential was written.
			u, uerr := svc.EnsureUser(context.Background(), connectLogin)
			if uerr != nil {
				t.Fatalf("EnsureUser: %v", uerr)
			}
			if u.ID != denied.ID {
				t.Errorf("EnsureUser id = %q, want the returned %q", u.ID, denied.ID)
			}
			set := settingsOf(t, svc, u.ID)
			if set.GitHubToken.Set {
				t.Error("a narrowed grant must not be stored as a repo credential")
			}
			if set.GitHub.Status != identity.GitHubDisconnected {
				t.Errorf("status = %q, want %q", set.GitHub.Status, identity.GitHubDisconnected)
			}
		})
	}
}

// The migration guarantee: a token typed into the old manual field keeps
// working and reads as connected. It is never reported as disconnected, and
// never as needing a reconnect just because nobody recorded its scopes.
func TestLegacyManualTokenCarriesForwardAsConnected(t *testing.T) {
	gh := &fakeGitHub{token: testGHToken, user: githubapi.GitHubUser{ID: 42, Login: connectGitHubLogin}}
	svc := connectService(t, gh)
	user, err := svc.EnsureUser(context.Background(), connectLogin)
	if err != nil {
		t.Fatalf("EnsureUser: %v", err)
	}

	// Exactly what the removed field did: PUT /api/settings with a token.
	if serr := svc.UpdateSettings(context.Background(), user.ID, identity.SettingsUpdate{
		GitHubToken: legacyToken,
	}); serr != nil {
		t.Fatalf("UpdateSettings: %v", serr)
	}

	set := settingsOf(t, svc, user.ID)
	if !set.GitHubToken.Set {
		t.Fatal("legacy token must stay stored")
	}
	if set.GitHub.Status != identity.GitHubUnknownScopes {
		t.Errorf("status = %q, want %q (stored, scopes unrecorded)",
			set.GitHub.Status, identity.GitHubUnknownScopes)
	}
	// And it is still the credential the runtime uses.
	if _, perr := svc.UpsertProject(context.Background(), user.ID, identity.ProjectUpdate{
		Name: "p", RepoURL: "https://github.com/acme/demo", WorkerCount: 1,
	}); perr != nil {
		t.Fatalf("UpsertProject: %v", perr)
	}
	proj, perr := svc.ProjectFor(context.Background(), user.ID)
	if perr != nil {
		t.Fatalf("ProjectFor: %v", perr)
	}
	rc, err := svc.RuntimeConfig(context.Background(), proj.ID)
	if err != nil {
		t.Fatalf("RuntimeConfig: %v", err)
	}
	if rc.GitHubAuthToken != legacyToken {
		t.Errorf("RuntimeConfig token = %q, want the legacy token", rc.GitHubAuthToken)
	}
}

// Verify is where an unclassified carried-over token gets its scopes recorded.
// A probe that comes back WITH repo confirms the card; one WITHOUT repo turns
// it into a reconnect prompt instead of leaving it silently broken.
func TestVerifyClassifiesCarriedForwardToken(t *testing.T) {
	cases := []struct {
		name       string
		probed     string
		probeErr   error
		wantStatus string
	}{
		{name: "repo scope confirms the connection", probed: scopeRepo, wantStatus: identity.GitHubConnected},
		{
			name: "missing repo scope asks for a reconnect", probed: "gist,read:user",
			wantStatus: identity.GitHubNeedsReconnect,
		},
		{name: "unreadable scopes stay unknown", probed: "", wantStatus: identity.GitHubUnknownScopes},
		{
			name: "a failed probe never downgrades", probed: scopeRepo,
			probeErr: errProbeFailed, wantStatus: identity.GitHubUnknownScopes,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gh := &fakeGitHub{
				token:        testGHToken,
				user:         githubapi.GitHubUser{ID: 42, Login: connectGitHubLogin},
				probedScopes: tc.probed,
				probeErr:     tc.probeErr,
			}
			svc := connectService(t, gh)
			svc.SetVerifier(&fakeVerifier{})
			user, err := svc.EnsureUser(context.Background(), connectLogin)
			if err != nil {
				t.Fatalf("EnsureUser: %v", err)
			}
			if err := svc.UpdateSettings(context.Background(), user.ID, identity.SettingsUpdate{
				GitHubToken: legacyToken,
			}); err != nil {
				t.Fatalf("UpdateSettings: %v", err)
			}

			if _, err := svc.Verify(context.Background(), user.ID); err != nil {
				t.Fatalf("Verify: %v", err)
			}

			// The probe is handed the DECRYPTED stored token, not the OAuth one.
			if gh.gotProbedToken != legacyToken {
				t.Errorf("probed token = %q, want the stored legacy token", gh.gotProbedToken)
			}
			if got := settingsOf(t, svc, user.ID).GitHub.Status; got != tc.wantStatus {
				t.Errorf("status = %q, want %q", got, tc.wantStatus)
			}
			// Whatever the classification, the credential itself survives.
			if !settingsOf(t, svc, user.ID).GitHubToken.Set {
				t.Error("classification must never drop the stored credential")
			}
		})
	}
}

// Replacing the token through the API must not let it inherit the previous
// token's scope record — otherwise a fresh, narrower PAT would keep showing the
// old token's green "connected".
func TestUpdateSettingsResetsScopeRecordOnNewToken(t *testing.T) {
	gh := &fakeGitHub{
		token: connectedToken,
		scope: scopeRepo,
		user:  githubapi.GitHubUser{ID: 42, Login: connectGitHubLogin},
	}
	svc := connectService(t, gh)
	user, err := svc.CompleteConnect(context.Background(), "code-1")
	if err != nil {
		t.Fatalf("CompleteConnect: %v", err)
	}
	if got := settingsOf(t, svc, user.ID).GitHub.Status; got != identity.GitHubConnected {
		t.Fatalf("status = %q, want connected after connect", got)
	}

	if err := svc.UpdateSettings(context.Background(), user.ID, identity.SettingsUpdate{
		GitHubToken: "gh-replacement-token",
	}); err != nil {
		t.Fatalf("UpdateSettings: %v", err)
	}

	set := settingsOf(t, svc, user.ID)
	if set.GitHub.Status != identity.GitHubUnknownScopes {
		t.Errorf("status = %q, want %q — the new token's scopes are unrecorded",
			set.GitHub.Status, identity.GitHubUnknownScopes)
	}
	if set.GitHub.Login != "" {
		t.Errorf("login = %q, want empty — it belonged to the replaced token", set.GitHub.Login)
	}
}

// Amika/Devin keys are untouched by all of this: they live in their own slots
// and a stored key keeps reading as configured, so nobody re-enters a working
// key because of the redesign.
func TestExistingProviderKeysSurviveUnchanged(t *testing.T) {
	gh := &fakeGitHub{token: testGHToken, user: githubapi.GitHubUser{ID: 42, Login: connectGitHubLogin}}
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
	gh.scope = scopeRepo
	if _, err := svc.CompleteConnect(context.Background(), "code-1"); err != nil {
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
