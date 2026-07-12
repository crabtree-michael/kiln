package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/crabtree-michael/kiln/backend/internal/identity"
)

// bootstrapInput is the env-shaped, already-resolved set of values the boot-time
// adoption seeds from (11 phase 2). It is an explicit struct — not a read of
// os.Getenv inside bootstrap — so the integration test can drive every path by
// value without mutating process env. serve() fills it from Config + the plain
// AMIKA_* env vars.
type bootstrapInput struct {
	GitHubUser        string // KILN_BOOTSTRAP_GITHUB_USER — the owner to adopt orphans into ("" ⇒ adoption skipped)
	RepoURL           string // GITHUB_REPO_URL else AMIKA_REPO_URL — the seeded project's repo
	AmikaSnapshot     string // AMIKA_SNAPSHOT
	WorkerCount       int    // KILN_WORKER_COUNT — clamped 1..10 into projects.worker_count
	AnthropicAPIKey   string // ANTHROPIC_API_KEY — seeded only when user_config's is unset
	AmikaAPIKey       string // AMIKA_API_KEY — seeded only when unset
	AmikaClaudeCredID string // AMIKA_CLAUDE_CRED_ID — seeded only when unset
	GitHubAuthToken   string // GITHUB_AUTH_TOKEN — seeded only when unset
}

const (
	colProjectID = "project_id"
	colUserID    = "user_id"

	// minSeedWorkerCount/maxSeedWorkerCount mirror the projects table CHECK
	// (worker_count BETWEEN 1 AND 10) the seeded value is clamped into.
	minSeedWorkerCount = 1
	maxSeedWorkerCount = 10
)

// errColumnMissing marks a tenant column absent from information_schema — a
// structural error (a migration didn't run), not a nullability state.
var errColumnMissing = errors.New("kiln: bootstrap tenant column missing")

// projectIDTables are the eight per-project tables adopted (project_id) in the
// single adoption tx. push_subscriptions (user_id) and push_user_settings are
// handled separately since they key on the user, not the project.
var projectIDTables = []string{
	"tickets", "workers", "outbox", "events",
	"messages", "notifications", "agent_turns", "steward_pokes",
}

// tenantColumn is one (table, column) the finalizer flips NOT NULL once every
// row is adopted. project_id for the eight board/runtime/agent/steward tables,
// user_id for push_subscriptions.
type tenantColumn struct{ table, column string }

// tenantColumns is every tenant column the finalizer inspects — the eight
// project_id tables plus push_subscriptions.user_id — derived from
// projectIDTables so the two lists never drift.
var tenantColumns = buildTenantColumns()

func buildTenantColumns() []tenantColumn {
	cols := make([]tenantColumn, 0, len(projectIDTables)+1)
	for _, table := range projectIDTables {
		cols = append(cols, tenantColumn{table, colProjectID})
	}
	return append(cols, tenantColumn{"push_subscriptions", colUserID})
}

// bootstrap runs once every boot, between applyMigrations and buildGraph
// (11 phase 2). It is idempotent: a second boot re-derives the same user/project
// (find-or-create), adopts zero remaining orphans, and re-flips already-NOT-NULL
// columns to no-ops.
//
// When identity is not mounted (idSvc == nil) the user/config/adoption steps are
// skipped — there is no cipher to store credentials with — but the NOT NULL
// finalizer still runs, so a fresh empty DB (all tenant tables empty) finalizes
// cleanly and a legacy DB with orphan rows is simply left nullable until the
// operator configures identity + KILN_BOOTSTRAP_GITHUB_USER.
func bootstrap(ctx context.Context, db *sql.DB, idSvc *identity.Service, in bootstrapInput, log *slog.Logger) error {
	if idSvc == nil {
		if in.GitHubUser != "" {
			log.Warn("bootstrap: KILN_BOOTSTRAP_GITHUB_USER set but identity is not mounted "+
				"(need GITHUB_OAUTH_CLIENT_ID, GITHUB_OAUTH_CLIENT_SECRET, KILN_SECRETS_KEY); "+
				"skipping user/config/adoption, running NOT NULL finalizer only",
				"github_user", in.GitHubUser)
		}
		return finalizeTenantColumns(ctx, db, log)
	}
	if in.GitHubUser != "" {
		if err := bootstrapFromEnv(ctx, db, idSvc, in, log); err != nil {
			return err
		}
	}
	return finalizeTenantColumns(ctx, db, log)
}

// bootstrapFromEnv ensures the owner user + their project exist, seeds any unset
// credentials from env, and adopts every orphan row into that owner/project.
func bootstrapFromEnv(
	ctx context.Context, db *sql.DB, idSvc *identity.Service, in bootstrapInput, log *slog.Logger,
) error {
	user, err := idSvc.EnsureUser(ctx, in.GitHubUser)
	if err != nil {
		return fmt.Errorf("kiln: bootstrap ensure user: %w", err)
	}
	proj, err := ensureBootstrapProject(ctx, idSvc, user.ID, in, log)
	if err != nil {
		return err
	}
	if err := seedUnsetConfig(ctx, idSvc, user.ID, in, log); err != nil {
		return err
	}
	return adoptOrphans(ctx, db, proj.ID, user.ID, log)
}

// ensureBootstrapProject returns the owner's existing project untouched, or
// creates one seeded from env when they have none (phase 1: one project per
// owner). An existing project is never overwritten — a dashboard-edited project
// survives every subsequent boot.
func ensureBootstrapProject(
	ctx context.Context, idSvc *identity.Service, userID string, in bootstrapInput, log *slog.Logger,
) (identity.Project, error) {
	proj, err := idSvc.ProjectFor(ctx, userID)
	if err == nil {
		return proj, nil
	}
	if !errors.Is(err, identity.ErrNotFound) {
		return identity.Project{}, fmt.Errorf("kiln: bootstrap project lookup: %w", err)
	}
	proj, err = idSvc.UpsertProject(ctx, userID, identity.ProjectUpdate{
		Name:          projectName(in.RepoURL),
		RepoURL:       in.RepoURL,
		AmikaSnapshot: in.AmikaSnapshot,
		WorkerCount:   clampWorkerCount(in.WorkerCount),
	})
	if err != nil {
		return identity.Project{}, fmt.Errorf("kiln: bootstrap create project: %w", err)
	}
	log.Info("bootstrap: created project", "project_id", proj.ID, "name", proj.Name)
	return proj, nil
}

// seedUnsetConfig writes each env credential ONLY into a user_config field that
// is currently unset — a dashboard-written value is never overwritten. Secret
// values are never logged. UpdateSettings itself treats an empty string as
// "leave unchanged", so passing "" for an already-set (or env-unset) field is a
// no-op; pick() collapses both conditions to "".
func seedUnsetConfig(
	ctx context.Context, idSvc *identity.Service, userID string, in bootstrapInput, log *slog.Logger,
) error {
	me, err := idSvc.Me(ctx, userID)
	if err != nil {
		return fmt.Errorf("kiln: bootstrap read config status: %w", err)
	}
	upd := identity.SettingsUpdate{
		AnthropicKey:      pick(me.Settings.AnthropicKey.Set, in.AnthropicAPIKey),
		AmikaKey:          pick(me.Settings.AmikaKey.Set, in.AmikaAPIKey),
		GitHubToken:       pick(me.Settings.GitHubToken.Set, in.GitHubAuthToken),
		AmikaClaudeCredID: pick(me.Settings.AmikaClaudeCredID != "", in.AmikaClaudeCredID),
	}
	if upd == (identity.SettingsUpdate{}) {
		return nil
	}
	if err := idSvc.UpdateSettings(ctx, userID, upd); err != nil {
		return fmt.Errorf("kiln: bootstrap seed config: %w", err)
	}
	log.Info("bootstrap: seeded unset user_config fields from env") // never logs the values
	return nil
}

// pick returns the env value only when the stored field is unset, so a seed
// never clobbers a dashboard-written credential.
func pick(alreadySet bool, envValue string) string {
	if alreadySet {
		return ""
	}
	return envValue
}

// adoptOrphans backfills every NULL tenant column in one transaction: each
// per-project table to projectID, push_subscriptions to userID, and a copy of
// the legacy global push_settings.mode into the owner's push_user_settings row.
func adoptOrphans(ctx context.Context, db *sql.DB, projectID, userID string, log *slog.Logger) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("kiln: bootstrap begin adoption tx: %w", err)
	}
	defer func() {
		if rerr := tx.Rollback(); rerr != nil && !errors.Is(rerr, sql.ErrTxDone) {
			log.Error("bootstrap: rollback adoption tx", "err", rerr)
		}
	}()
	for _, table := range projectIDTables {
		//nolint:gosec // table names come from a fixed in-code allowlist, never user input
		q := fmt.Sprintf(`UPDATE %s SET project_id = $1 WHERE project_id IS NULL`, table)
		if _, err := tx.ExecContext(ctx, q, projectID); err != nil {
			return fmt.Errorf("kiln: bootstrap adopt %s: %w", table, err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE push_subscriptions SET user_id = $1 WHERE user_id IS NULL`, userID); err != nil {
		return fmt.Errorf("kiln: bootstrap adopt push_subscriptions: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO push_user_settings (user_id, mode)
		 SELECT $1, mode FROM push_settings WHERE id = 1
		 ON CONFLICT (user_id) DO NOTHING`, userID); err != nil {
		return fmt.Errorf("kiln: bootstrap copy push settings: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("kiln: bootstrap commit adoption: %w", err)
	}
	return nil
}

// finalizeTenantColumns flips each still-nullable tenant column to NOT NULL once
// no NULL rows remain. It runs every boot and is idempotent: an already-NOT-NULL
// column is skipped. A column that still has NULL rows (e.g. prod before the
// operator sets KILN_BOOTSTRAP_GITHUB_USER) is logged and left nullable — it
// must never fail the boot.
func finalizeTenantColumns(ctx context.Context, db *sql.DB, log *slog.Logger) error {
	for _, tc := range tenantColumns {
		nullable, err := columnIsNullable(ctx, db, tc.table, tc.column)
		if err != nil {
			return err
		}
		if !nullable {
			continue // already NOT NULL — idempotent no-op
		}
		hasNull, err := hasNullRows(ctx, db, tc.table, tc.column)
		if err != nil {
			return err
		}
		if hasNull {
			log.Warn("bootstrap: leaving column nullable, NULL rows remain (set KILN_BOOTSTRAP_GITHUB_USER to adopt)",
				"table", tc.table, "column", tc.column)
			continue
		}
		alter := fmt.Sprintf(`ALTER TABLE %s ALTER COLUMN %s SET NOT NULL`, tc.table, tc.column)
		if _, err := db.ExecContext(ctx, alter); err != nil {
			return fmt.Errorf("kiln: bootstrap finalize %s.%s: %w", tc.table, tc.column, err)
		}
		log.Info("bootstrap: column finalized NOT NULL", "table", tc.table, "column", tc.column)
	}
	return nil
}

// columnIsNullable reports whether the column is currently declared nullable, via
// information_schema in the current schema (the finalizer's idempotency gate).
func columnIsNullable(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	var isNullable string
	err := db.QueryRowContext(ctx, `
		SELECT is_nullable FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = $1 AND column_name = $2`,
		table, column).Scan(&isNullable)
	if errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("%w: %s.%s", errColumnMissing, table, column)
	}
	if err != nil {
		return false, fmt.Errorf("kiln: bootstrap inspect %s.%s: %w", table, column, err)
	}
	return isNullable == "YES", nil
}

// hasNullRows reports whether any row still has a NULL in the column.
func hasNullRows(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	var exists bool
	//nolint:gosec // table/column come from the fixed tenantColumns allowlist, never user input
	q := fmt.Sprintf(`SELECT EXISTS (SELECT 1 FROM %s WHERE %s IS NULL)`, table, column)
	if err := db.QueryRowContext(ctx, q).Scan(&exists); err != nil {
		return false, fmt.Errorf("kiln: bootstrap check nulls %s.%s: %w", table, column, err)
	}
	return exists, nil
}

// projectName derives the seeded project name from the repo URL: its basename
// with a trailing .git stripped (handling both https and scp-style git@ URLs),
// falling back to "kiln" when the URL is empty or yields nothing.
func projectName(repoURL string) string {
	u := strings.TrimSuffix(strings.TrimSpace(repoURL), "/")
	if u == "" {
		return "kiln"
	}
	if i := strings.LastIndexAny(u, "/:"); i >= 0 {
		u = u[i+1:]
	}
	u = strings.TrimSuffix(u, ".git")
	if u == "" {
		return "kiln"
	}
	return u
}

// clampWorkerCount pins the seeded worker_count into the projects table's
// CHECK range (BETWEEN 1 AND 10).
func clampWorkerCount(n int) int {
	switch {
	case n < minSeedWorkerCount:
		return minSeedWorkerCount
	case n > maxSeedWorkerCount:
		return maxSeedWorkerCount
	default:
		return n
	}
}
