-- Amika base URL is now a platform env var (AMIKA_BASE_URL), not per-user
-- config (design change, 11 §3 amended 2026-07-06).
ALTER TABLE user_config DROP COLUMN amika_base_url;
