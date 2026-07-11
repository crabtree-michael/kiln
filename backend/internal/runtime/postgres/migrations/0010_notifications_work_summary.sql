-- Work summary on the mechanical "done" completion card (08 §7): the one-line
-- description of the landed work — the commit subject under the main gate, or the
-- pull-request title under the PR gate — so the card says WHAT shipped without a
-- trip to GitHub. NULL on every other kind, and on a completion card whose summary
-- could not be read.
ALTER TABLE notifications ADD COLUMN work_summary text NULL;
