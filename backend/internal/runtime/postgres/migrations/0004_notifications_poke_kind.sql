-- Allow the mechanical stall-nudge card kind (steward module): a feed-only
-- "poke" notification whose body is empty and whose ticket title carries a 👉.
-- Widens the notifications.kind CHECK from ('update','preview') to add 'poke'.
-- The inline column CHECK from 0003 is named notifications_kind_check by
-- Postgres' default constraint naming.

ALTER TABLE notifications DROP CONSTRAINT notifications_kind_check;
ALTER TABLE notifications ADD CONSTRAINT notifications_kind_check
  CHECK (kind IN ('update', 'preview', 'poke'));
