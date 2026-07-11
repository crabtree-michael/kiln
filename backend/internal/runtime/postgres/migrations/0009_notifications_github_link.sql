-- GitHub link on the mechanical "done" completion card (08 §7): when a ticket is
-- accepted to Done the feed card carries a link to the landed work — the commit
-- on origin/main under merge-on-main, or the pull request under the PR gate — so
-- the card's second line is a clickable reference into GitHub. github_url is the
-- web page; github_label is the clickable text (abbreviated SHA or "#<number>").
-- Both NULL on every other kind, and on a completion card with no link available.
ALTER TABLE notifications ADD COLUMN github_url   text NULL;
ALTER TABLE notifications ADD COLUMN github_label text NULL;
