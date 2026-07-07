-- user_id: tenant column (11 §3). Nullable until boot-time adoption
-- backfills every row and flips it NOT NULL (cmd/kiln bootstrap, a later
-- task). Plain uuid carried by value — no FK across module migration sets
-- (03 I8).
ALTER TABLE push_subscriptions ADD COLUMN user_id uuid;

-- Replaces the push_settings singleton (0002_push_settings.sql) with a
-- per-user row now that push is a tenant concern. push_settings is kept in
-- place, unread after 11 phase 2; its mode is copied to the bootstrap owner
-- at adoption.
CREATE TABLE push_user_settings (
    user_id uuid PRIMARY KEY,
    mode    text NOT NULL DEFAULT 'blocked'
);
