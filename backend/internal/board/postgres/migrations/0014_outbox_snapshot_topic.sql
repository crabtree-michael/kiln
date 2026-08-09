-- Widen the outbox topic CHECK to admit agent.snapshot (05 §4, §6): the emission
-- an exit from Developing makes in place of agent.release when the ticket's
-- sandbox is saved (KeepSandbox) — capture the workspace as a reusable base
-- image instead of recycling the slot. 0006 last widened this constraint; without
-- this one every accept/delete of a saved-sandbox ticket would fail the CHECK,
-- which is exactly how feed.completion broke every "done" transition (see 0006's
-- header). Drop the constraint and re-add the widened form, mirroring
-- 0004_outbox_topics.sql.

ALTER TABLE outbox DROP CONSTRAINT outbox_topic_check;

ALTER TABLE outbox ADD CONSTRAINT outbox_topic_check CHECK (topic IN (
  'agent.send','agent.release','agent.snapshot','notify.send','pull.evaluate',
  'board.updated','feed.updated','activity.toast','feed.completion'
));
