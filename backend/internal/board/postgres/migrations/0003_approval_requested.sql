-- approval_requested: a shaping ticket the brain has surfaced for the user to
-- approve (08 §5 proposal card). True only while shaping; MarkReady clears it.
-- Migrations apply in filename order (02 §14).

ALTER TABLE tickets
  ADD COLUMN approval_requested boolean NOT NULL DEFAULT false;

-- The fact only makes sense in Backlog · Shaping (08 §B.1): once the ticket
-- leaves shaping the proposal is resolved, so approval_requested must be false.
ALTER TABLE tickets
  ADD CONSTRAINT approval_requested_only_shaping
  CHECK (NOT approval_requested OR state = 'shaping');
