-- Retained feed history (08 D2′): the feed no longer drains seen updates — it
-- shows unretracted notifications (seen AND unseen) as a paginated, newest-first
-- history with a last-seen divider. The hot scans are now:
--   RecentNotifications / HistoryBefore -> WHERE retracted_at IS NULL ... id DESC
-- A partial index on the live (unretracted) rows keeps those keyset pages cheap
-- as history grows unbounded (retention/pruning is a deliberate follow-up, not
-- built here). The 0003 notifications_recent (id DESC over all rows) still serves
-- the MarkSeen high-water UPDATE and the brain's unseen list.

CREATE INDEX notifications_active_recent
  ON notifications (id DESC)
  WHERE retracted_at IS NULL;
