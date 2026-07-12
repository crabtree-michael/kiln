-- Per-project coding-agent provider selection (multi-provider design §7, §9, D4/D7).
-- The registry key (amika, devin, mock, …) the project's turns run on; empty means
-- "use the deployment default" (AGENT_MODE), which is exactly the back-compat
-- behavior for every existing single-provider project — no backfill needed.
ALTER TABLE projects
    ADD COLUMN agent_provider text NOT NULL DEFAULT '';
