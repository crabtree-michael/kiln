-- Per-project merge gate mode (06 §7): which condition satisfies a ticket's
-- merge gate. 'main' (the prior, hardcoded behavior) accepts a done only once
-- the commit is on origin/main; 'pr' accepts it once the work is in a pull
-- request. Existing rows default to 'main', so no behavior change on upgrade.
ALTER TABLE projects
  ADD COLUMN merge_gate_mode text NOT NULL DEFAULT 'main'
    CHECK (merge_gate_mode IN ('main', 'pr'));
