-- done_commit: the origin/main commit SHA AcceptToDone records for a ticket's
-- work. Nullable — set only when a ticket is accepted with a commit (the
-- brain's merge gate, 06 §7, always supplies one; direct board callers may
-- not). One commit maps to at most one ticket per project, so a SHA already
-- spent on another ticket cannot mark a second one done: the service refuses
-- with ErrCommitAlreadyUsed (03 §4) and the partial unique index below is the
-- DB backstop even if service code is wrong (03 §6).
-- Migrations apply in filename order (02 §14).

ALTER TABLE tickets ADD COLUMN done_commit text;

CREATE UNIQUE INDEX tickets_done_commit_unique
    ON tickets (project_id, done_commit)
    WHERE done_commit IS NOT NULL;
