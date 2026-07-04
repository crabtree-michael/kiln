-- Widen the outbox topic CHECK to admit the two 08 §5 emissions: feed.updated
-- (reassemble the feed) and activity.toast (an ephemeral activity pill). The
-- constraint in 0002_outbox.sql was created inline and unnamed, so Postgres
-- auto-named it outbox_topic_check — drop it and re-add the widened form.

ALTER TABLE outbox DROP CONSTRAINT outbox_topic_check;

ALTER TABLE outbox ADD CONSTRAINT outbox_topic_check CHECK (topic IN (
  'agent.send','agent.release','notify.send','pull.evaluate','board.updated',
  'feed.updated','activity.toast'
));
