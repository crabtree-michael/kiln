-- Widen the outbox topic CHECK to admit feed.completion (08 §7): the persistent
-- "done" feed card AcceptToDone emits alongside the ephemeral finished
-- activity.toast. 0004 last widened this constraint; AcceptToDone's new emission
-- (05 §4) was left out, so every "done" transition failed the CHECK. Drop the
-- constraint and re-add the widened form, mirroring 0004_outbox_topics.sql.

ALTER TABLE outbox DROP CONSTRAINT outbox_topic_check;

ALTER TABLE outbox ADD CONSTRAINT outbox_topic_check CHECK (topic IN (
  'agent.send','agent.release','notify.send','pull.evaluate','board.updated',
  'feed.updated','activity.toast','feed.completion'
));
