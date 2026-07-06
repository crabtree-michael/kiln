-- Allow the mechanical completion card kind (08 §7): a feed-only "done"
-- notification whose body is empty and whose ticket title carries a ✅ — the
-- persistent counterpart to the ephemeral "finished" toast, posted by the
-- runtime's feed.completion handler. Its own kind (rather than reusing "update")
-- so it renders single-line like a poke and stays out of the brain's editable
-- update list and the unseen-updates badge. Widens the notifications.kind CHECK
-- from ('update','preview','poke') to add 'done'.

ALTER TABLE notifications DROP CONSTRAINT notifications_kind_check;
ALTER TABLE notifications ADD CONSTRAINT notifications_kind_check
  CHECK (kind IN ('update', 'preview', 'poke', 'done'));
