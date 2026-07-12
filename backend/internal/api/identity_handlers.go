package api

// The signed-in account surface (11 §4): GET /api/me, PUT /api/settings,
// PUT /api/project, POST /api/settings/verify — all session-protected via
// withSession — plus the dev-only POST /api/dev/session mint (11 §7).
// Mounted only when EnableIdentity was called (see routes.go).

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/crabtree-michael/kiln/backend/internal/identity"
	"github.com/crabtree-michael/kiln/backend/internal/wire"
)

// handleMe returns the caller's account view (11 §4): user, project (absent
// until onboarding creates it), and config status — secrets as
// presence+fingerprint only, never values.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request, user identity.User) {
	s.writeMe(w, r, user.ID)
}

// handlePutSettings applies a partial credential update (11 §4): empty or
// omitted fields are unchanged (write-only secrets), then returns the
// refreshed account view.
func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request, user identity.User) {
	var req wire.SettingsUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	upd := identity.SettingsUpdate{
		AnthropicKey:      derefOr(req.AnthropicApiKey, ""),
		AmikaKey:          derefOr(req.AmikaApiKey, ""),
		DevinKey:          derefOr(req.DevinApiKey, ""),
		GitHubToken:       derefOr(req.GithubAuthToken, ""),
		AmikaClaudeCredID: derefOr(req.AmikaClaudeCredId, ""),
	}
	if err := s.account.UpdateSettings(r.Context(), user.ID, upd); err != nil {
		slog.Error("api: update settings", "err", err)
		http.Error(w, "update settings", http.StatusInternalServerError)
		return
	}
	s.writeMe(w, r, user.ID)
}

// handlePutProject creates or updates the caller's single project (11 §3–§4),
// then returns the refreshed account view. A write rejected by the domain's
// validation (identity.ErrInvalidProject) is the client's fault: 400.
func (s *Server) handlePutProject(w http.ResponseWriter, r *http.Request, user identity.User) {
	var req wire.ProjectUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	upd := identity.ProjectUpdate{
		Name:          req.Name,
		RepoURL:       req.RepoUrl,
		AgentProvider: derefOr(req.AgentProvider, ""),
		AmikaSnapshot: derefOr(req.AmikaSnapshot, ""),
		BrainModel:    derefOr(req.BrainModel, ""),
		WorkerCount:   derefOr(req.WorkerCount, 0),
		MergeGateMode: mergeGateModeToDomain(req.MergeGateMode),
		AmikaSecrets:  amikaSecretsToDomain(req.AmikaSecrets),
	}
	if _, err := s.account.UpsertProject(r.Context(), user.ID, upd); err != nil {
		if errors.Is(err, identity.ErrInvalidProject) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		slog.Error("api: upsert project", "err", err)
		http.Error(w, "upsert project", http.StatusInternalServerError)
		return
	}
	s.writeMe(w, r, user.ID)
}

// handleVerify runs the live connection checks over the caller's stored
// credentials (11 §4) and returns them in the service's order
// (anthropic, amika, devin, repo). No request body.
func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request, user identity.User) {
	checks, err := s.account.Verify(r.Context(), user.ID)
	if err != nil {
		slog.Error("api: verify settings", "err", err)
		http.Error(w, "verify settings", http.StatusInternalServerError)
		return
	}
	out := make([]wire.VerifyCheck, 0, len(checks))
	for _, c := range checks {
		out = append(out, wire.VerifyCheck{
			Name:    wire.VerifyCheckName(c.Name),
			Status:  wire.VerifyCheckStatus(c.Status),
			Message: c.Message,
		})
	}
	writeJSON(w, http.StatusOK, wire.VerifyResponse{Checks: out})
}

// handleDevSession mints a session straight from a GitHub login (dev only),
// bypassing the real OAuth dance, so an e2e can establish an authenticated
// session deterministically (11 §7). Sets the same kiln_session cookie the
// OAuth callback would and returns {token, expires_at} for non-browser
// clients. Mounted only when EnableDevSession was called
// (KILN_DEV_ENDPOINTS); NOT part of the wire contract (/schema).
func (s *Server) handleDevSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		GitHubLogin string `json:"github_login"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.GitHubLogin == "" {
		http.Error(w, "invalid request body: github_login required", http.StatusBadRequest)
		return
	}
	user, err := s.devSession.DevSignIn(r.Context(), req.GitHubLogin)
	if err != nil {
		slog.Error("api: dev sign-in", "err", err)
		http.Error(w, "dev sign-in", http.StatusInternalServerError)
		return
	}
	token, expires, err := s.devSession.CreateSession(r.Context(), user.ID)
	if err != nil {
		slog.Error("api: dev create session", "err", err)
		http.Error(w, "create session", http.StatusInternalServerError)
		return
	}
	setCookie(w, r, sessionCookie, token, time.Until(expires))
	writeJSON(w, http.StatusOK, map[string]any{"token": token, "expires_at": expires})
}

// writeMe fetches the caller's account view and writes it as a 200 wire.Me —
// the shared tail of handleMe and both PUT handlers (one round-trip refresh
// after a write, matching the Task 1 contract).
func (s *Server) writeMe(w http.ResponseWriter, r *http.Request, userID string) {
	me, err := s.account.Me(r.Context(), userID)
	if err != nil {
		slog.Error("api: read account view", "err", err)
		http.Error(w, "read account", http.StatusInternalServerError)
		return
	}
	out := meToWire(me)
	// The provider descriptors are deployment-global composition-root data, not
	// part of the identity account view — the dashboard reads them to render its
	// provider select (multi-provider design §8). Omitted when none were enabled.
	if len(s.providers) > 0 {
		descriptors := s.providers
		out.Providers = &descriptors
	}
	writeJSON(w, http.StatusOK, out)
}

// meToWire maps an identity.Me onto the generated wire.Me (11 §4) — the
// single domain→wire mapper for the account view, next to boardToWire's
// precedent. A nil Project stays nil (omitted from the JSON); each secret
// carries through as presence+fingerprint only. Structurally no secret value
// can appear: identity.Me carries none.
func meToWire(me identity.Me) wire.Me {
	out := wire.Me{
		User: wire.MeUser{
			GithubLogin: me.User.GitHubLogin,
			DisplayName: me.User.DisplayName,
			AvatarUrl:   me.User.AvatarURL,
		},
		Settings: wire.MeSettings{
			AnthropicApiKey:   secretToWire(me.Settings.AnthropicKey),
			AmikaApiKey:       secretToWire(me.Settings.AmikaKey),
			DevinApiKey:       secretToWire(me.Settings.DevinKey),
			GithubAuthToken:   secretToWire(me.Settings.GitHubToken),
			AmikaClaudeCredId: me.Settings.AmikaClaudeCredID,
		},
	}
	if p := me.Project; p != nil {
		out.Project = &wire.MeProject{
			Name:          p.Name,
			RepoUrl:       p.RepoURL,
			AgentProvider: p.AgentProvider,
			AmikaSnapshot: p.AmikaSnapshot,
			BrainModel:    p.BrainModel,
			WorkerCount:   p.WorkerCount,
			MergeGateMode: mergeGateModeToWire(p.MergeGateMode),
			AmikaSecrets:  amikaSecretsToWire(me.ProjectSecrets),
		}
	}
	return out
}

// amikaSecretsToWire maps the project's secret statuses to the wire read type.
// Always non-nil (the wire field is a required array) so it marshals to [] not
// null. The name is a label (safe to expose); the value is presence+fingerprint
// only — never the secret itself (02 §8, 11 §3 D7).
func amikaSecretsToWire(secrets []identity.AmikaSecretStatus) []wire.AmikaSecret {
	out := make([]wire.AmikaSecret, 0, len(secrets))
	for _, s := range secrets {
		out = append(out, wire.AmikaSecret{Name: s.Name, Value: secretToWire(s.Value)})
	}
	return out
}

// amikaSecretsToDomain maps an optional inbound secret list to the domain type;
// an omitted field (nil) becomes a nil slice — zero secrets. Trimming, the
// write-only value merge, and validation are the service's job
// (identity.mergeAmikaSecrets).
func amikaSecretsToDomain(secrets *[]wire.AmikaSecretInput) []identity.AmikaSecretInput {
	if secrets == nil {
		return nil
	}
	out := make([]identity.AmikaSecretInput, 0, len(*secrets))
	for _, s := range *secrets {
		out = append(out, identity.AmikaSecretInput{Name: s.Name, Value: derefOr(s.Value, "")})
	}
	return out
}

// secretToWire maps one stored secret's presence+fingerprint (11 §3 D7).
func secretToWire(s identity.SecretStatus) wire.SecretStatus {
	return wire.SecretStatus{Set: s.Set, Tail: s.Tail}
}

// mergeGateModeToDomain maps the optional inbound gate mode to the domain type;
// an omitted field (nil) becomes "" so the service defaults it to MergeGateMain.
// An unknown value passes through for the service to reject (ErrInvalidProject).
func mergeGateModeToDomain(m *wire.ProjectUpdateRequestMergeGateMode) identity.MergeGateMode {
	if m == nil {
		return ""
	}
	return identity.MergeGateMode(*m)
}

// mergeGateModeToWire maps the stored gate mode to the wire read type, defaulting
// an unset value (legacy rows) to "main" so the field is always a valid enum.
func mergeGateModeToWire(m identity.MergeGateMode) wire.MeProjectMergeGateMode {
	if m == "" {
		return wire.MeProjectMergeGateModeMain
	}
	return wire.MeProjectMergeGateMode(m)
}

// derefOr dereferences an optional oapi-codegen pointer field, falling back
// when absent.
func derefOr[T any](p *T, fallback T) T {
	if p == nil {
		return fallback
	}
	return *p
}
