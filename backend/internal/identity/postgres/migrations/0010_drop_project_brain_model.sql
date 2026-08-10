-- Drop the orphaned per-project brain model column (06 §2: the brain model is
-- backend-only, KILN_BRAIN_MODEL). ae9e26a removed the field from every layer
-- but deleted the column from 0001_identity.sql in place instead of adding a
-- drop, and the runner keys its ledger on filename with no checksum — so 0001
-- never re-ran. Databases created before that commit still physically carry
-- `brain_model text NOT NULL DEFAULT ''` (inert: every write enumerates its
-- columns), fresh ones never had it. IF EXISTS converges both in one file.
ALTER TABLE projects
    DROP COLUMN IF EXISTS brain_model;
