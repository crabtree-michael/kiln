-- project_id: tenant column (11 §3). Nullable until boot-time adoption
-- backfills every row and flips it NOT NULL (cmd/kiln bootstrap, a later
-- task). Plain uuid carried by value — no FK across module migration sets
-- (03 I8).

ALTER TABLE agent_turns ADD COLUMN project_id uuid;
