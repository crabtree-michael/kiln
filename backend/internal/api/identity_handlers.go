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

// handlePutProject is the back-compat singular write (11 §3–§4, 12 §9): it
// creates-or-updates the caller's FIRST project, then returns the refreshed
// account view. A write rejected by the domain's validation
// (identity.ErrInvalidProject) is the client's fault: 400. New clients use the
// id'd /api/projects endpoints below.
func (s *Server) handlePutProject(w http.ResponseWriter, r *http.Request, user identity.User) {
	upd, ok := decodeProjectUpdate(w, r)
	if !ok {
		return
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

// handleListProjects returns the caller's live projects (GET /api/projects,
// 12 §3.1) — each with its id and secret statuses.
func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request, user identity.User) {
	views, err := s.account.ListProjects(r.Context(), user.ID)
	if err != nil {
		slog.Error("api: list projects", "err", err)
		http.Error(w, "list projects", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, projectViewsToWire(views))
}

// handleCreateProject creates a distinct project for the caller (POST
// /api/projects, 12 DP2) and returns it (201) with its server-generated id. A
// domain-rejected write is a 400.
func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request, user identity.User) {
	upd, ok := decodeProjectUpdate(w, r)
	if !ok {
		return
	}
	view, err := s.account.CreateProject(r.Context(), user.ID, upd)
	if err != nil {
		if errors.Is(err, identity.ErrInvalidProject) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		slog.Error("api: create project", "err", err)
		http.Error(w, "create project", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusCreated, projectViewToWire(view))
}

// handleUpdateProject updates a project the caller owns (PUT /api/projects/{pid},
// 12 §3.1) and returns it. A project the caller does not own is ErrNotFound → 404
// (never confirming a foreign project's existence, §3.2); a domain-rejected
// write is a 400.
func (s *Server) handleUpdateProject(w http.ResponseWriter, r *http.Request, user identity.User) {
	upd, ok := decodeProjectUpdate(w, r)
	if !ok {
		return
	}
	view, err := s.account.UpdateProject(r.Context(), user.ID, r.PathValue(projectIDParam), upd)
	if err != nil {
		switch {
		case errors.Is(err, identity.ErrNotFound):
			writeNotFound(w, msgNoSuchProject)
		case errors.Is(err, identity.ErrInvalidProject):
			http.Error(w, err.Error(), http.StatusBadRequest)
		default:
			slog.Error("api: update project", "err", err)
			http.Error(w, "update project", http.StatusInternalServerError)
		}
		return
	}
	writeJSON(w, http.StatusOK, projectViewToWire(view))
}

// handleDeleteProject deletes a project the caller owns (DELETE
// /api/projects/{pid}, 12 §5): the composition-root coordinator runs the
// application-level cascade (purge state, evict tenant + repo clone) then
// soft-deletes the row. A project the caller does not own is ErrNotFound → 404,
// and the cascade never runs. 204 on success.
func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request, user identity.User) {
	err := s.projectDel.DeleteProject(r.Context(), user.ID, r.PathValue(projectIDParam))
	if err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			writeNotFound(w, msgNoSuchProject)
			return
		}
		slog.Error("api: delete project", "err", err)
		http.Error(w, "delete project", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleVerify runs the live connection checks over the caller's stored
// credentials against their first project's repo (11 §4). No request body.
func (s *Server) handleVerify(w http.ResponseWriter, r *http.Request, user identity.User) {
	checks, err := s.account.Verify(r.Context(), user.ID)
	if err != nil {
		slog.Error("api: verify settings", "err", err)
		http.Error(w, "verify settings", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, verifyChecksToWire(checks))
}

// handleVerifyProject runs the checks against a specific project's repo (POST
// /api/projects/{pid}/verify, 12 §3.1): the repo check is per-project, the
// credential checks per-user. A project the caller does not own is 404.
func (s *Server) handleVerifyProject(w http.ResponseWriter, r *http.Request, user identity.User) {
	checks, err := s.account.VerifyProject(r.Context(), user.ID, r.PathValue(projectIDParam))
	if err != nil {
		if errors.Is(err, identity.ErrNotFound) {
			writeNotFound(w, msgNoSuchProject)
			return
		}
		slog.Error("api: verify project", "err", err)
		http.Error(w, "verify project", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, verifyChecksToWire(checks))
}

// decodeProjectUpdate decodes a ProjectUpdateRequest body into the domain type,
// writing a 400 and returning ok=false on a malformed body. Shared by the
// singular, create, and update handlers.
func decodeProjectUpdate(w http.ResponseWriter, r *http.Request) (identity.ProjectUpdate, bool) {
	var req wire.ProjectUpdateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return identity.ProjectUpdate{}, false
	}
	return identity.ProjectUpdate{
		Name:          req.Name,
		RepoURL:       req.RepoUrl,
		AgentProvider: derefOr(req.AgentProvider, ""),
		AmikaSnapshot: derefOr(req.AmikaSnapshot, ""),
		WorkerCount:   derefOr(req.WorkerCount, 0),
		MergeGateMode: mergeGateModeToDomain(req.MergeGateMode),
		AmikaSecrets:  amikaSecretsToDomain(req.AmikaSecrets),
	}, true
}

// verifyChecksToWire maps the service's check results onto the wire response,
// preserving order (anthropic, amika, devin, repo). Shared by the user- and
// project-scoped verify handlers.
func verifyChecksToWire(checks []identity.CheckResult) wire.VerifyResponse {
	out := make([]wire.VerifyCheck, 0, len(checks))
	for _, c := range checks {
		out = append(out, wire.VerifyCheck{
			Name:    wire.VerifyCheckName(c.Name),
			Status:  wire.VerifyCheckStatus(c.Status),
			Message: c.Message,
		})
	}
	return wire.VerifyResponse{Checks: out}
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

// meToWire maps an identity.Me onto the generated wire.Me (11 §4, 12 §3.1) —
// the single domain→wire mapper for the account view, next to boardToWire's
// precedent. The old singular project? became a projects[] (12 §3.1); each
// carries its id and its secrets as presence+fingerprint only. Structurally no
// secret value can appear: identity.Me carries none.
func meToWire(me identity.Me) wire.Me {
	return wire.Me{
		User: wire.MeUser{
			GithubLogin: me.User.GitHubLogin,
			DisplayName: me.User.DisplayName,
			AvatarUrl:   me.User.AvatarURL,
		},
		Projects: projectViewsToWire(me.Projects),
		Settings: wire.MeSettings{
			AnthropicApiKey:   secretToWire(me.Settings.AnthropicKey),
			AmikaApiKey:       secretToWire(me.Settings.AmikaKey),
			DevinApiKey:       secretToWire(me.Settings.DevinKey),
			GithubAuthToken:   secretToWire(me.Settings.GitHubToken),
			AmikaClaudeCredId: me.Settings.AmikaClaudeCredID,
		},
	}
}

// projectViewsToWire maps the caller's project views onto wire.MeProject,
// always non-nil so the JSON is an array (never null) — the client's "not
// onboarded" discriminator is projects.length === 0 (12 §4.1).
func projectViewsToWire(views []identity.ProjectView) []wire.MeProject {
	out := make([]wire.MeProject, 0, len(views))
	for _, v := range views {
		out = append(out, projectViewToWire(v))
	}
	return out
}

// projectViewToWire maps one project view (project + secret statuses) onto the
// wire.MeProject, including the id the client keys every project-scoped call by
// (12 §3.1, DP5).
func projectViewToWire(v identity.ProjectView) wire.MeProject {
	p := v.Project
	return wire.MeProject{
		Id:            p.ID,
		Name:          p.Name,
		RepoUrl:       p.RepoURL,
		AgentProvider: p.AgentProvider,
		AmikaSnapshot: p.AmikaSnapshot,
		WorkerCount:   p.WorkerCount,
		MergeGateMode: mergeGateModeToWire(p.MergeGateMode),
		AmikaSecrets:  amikaSecretsToWire(v.Secrets),
	}
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
