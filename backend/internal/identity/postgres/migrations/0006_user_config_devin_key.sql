-- The Devin API key is now a per-user credential the dashboard manages
-- (multi-provider design §8), mirroring amika_api_key_enc. NULL = unset, so
-- existing rows and deployments still on the DEVIN_API_KEY env default need no
-- backfill. AES-GCM ciphertext, exactly like the other *_enc columns.
ALTER TABLE user_config ADD COLUMN devin_api_key_enc bytea;
