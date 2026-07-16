-- Multi-project support: docs/specs/12-multi-project.md §2 (DP1, DP6).
-- Drop the "one project per user" pin (12 §1.1, planned by 11 §3 as a
-- drop-not-migrate) and add the nullable soft-delete column every project
-- read path now filters on (NULL = live; set = soft-deleted, retained).
DROP INDEX one_project_per_owner;
ALTER TABLE projects ADD COLUMN deleted_at timestamptz;
