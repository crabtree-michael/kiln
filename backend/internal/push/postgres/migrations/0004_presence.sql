-- last_seen_foreground_at: the device's foreground-presence lease (02 §10 push
-- dedup design). Stamped with the server clock on each POST /api/presence
-- heartbeat while the tab is visible, and nulled on the clean leave beacon. The
-- sender skips a device whose lease is still fresh (within presenceTTL) so the
-- in-app toast is the only surface, avoiding a duplicate OS banner.
--
-- Nullable with no backfill on purpose: NULL means "never/not foreground",
-- which the sender reads as *send* — the safe, send-by-default fallback. Every
-- existing row starts NULL, so the feature is inert (exactly today's behavior)
-- until the client begins reporting presence. Additive, no-downtime.
ALTER TABLE push_subscriptions
  ADD COLUMN last_seen_foreground_at timestamptz;
