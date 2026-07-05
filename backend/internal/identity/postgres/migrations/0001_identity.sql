-- Identity & per-user config: docs/specs/11-multi-user.md §3 (phase 1).
CREATE TABLE users (
  id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  github_id    bigint NOT NULL UNIQUE,
  github_login text NOT NULL UNIQUE
               CHECK (github_login = lower(github_login) AND github_login <> ''),
  display_name text NOT NULL DEFAULT '',
  avatar_url   text NOT NULL DEFAULT '',
  created_at   timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
  token_hash text PRIMARY KEY,
  user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now(),
  expires_at timestamptz NOT NULL
);
CREATE INDEX sessions_by_user ON sessions (user_id);

-- Credentials follow the person (11 §3 D4); *_enc columns are AES-GCM
-- ciphertext (11 §3 D7), NULL = unset. Non-secrets stored in the clear.
CREATE TABLE user_config (
  user_id               uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  anthropic_api_key_enc bytea,
  amika_api_key_enc     bytea,
  github_auth_token_enc bytea,
  amika_base_url        text NOT NULL DEFAULT '',
  amika_claude_cred_id  text NOT NULL DEFAULT '',
  updated_at            timestamptz NOT NULL DEFAULT now()
);

-- One brain per project (11 §3 D5); repo + brain knobs ride the project.
CREATE TABLE projects (
  id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  owner_user_id  uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name           text NOT NULL CHECK (name <> ''),
  repo_url       text NOT NULL DEFAULT '',
  amika_snapshot text NOT NULL DEFAULT '',
  brain_model    text NOT NULL DEFAULT '',
  worker_count   int  NOT NULL DEFAULT 3 CHECK (worker_count BETWEEN 1 AND 10),
  created_at     timestamptz NOT NULL DEFAULT now()
);
-- One project per user in phase 1 (11 §3): drop this index — no data
-- migration — when multi-project lands.
CREATE UNIQUE INDEX one_project_per_owner ON projects (owner_user_id);
