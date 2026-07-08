-- Per-project secrets injected as env vars into every sandbox the project
-- starts (02 §8). Each element is {"name_enc": bytea, "value_enc": bytea}
-- (base64 in JSON) — the env-var name and its value, both AES-GCM-encrypted at
-- rest like the other credentials (11 §3 D7); the value is write-only.
ALTER TABLE projects
  ADD COLUMN amika_secrets jsonb NOT NULL DEFAULT '[]'::jsonb;
