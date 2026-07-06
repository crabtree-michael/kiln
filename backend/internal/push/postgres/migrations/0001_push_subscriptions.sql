-- Web Push subscriptions: docs/specs/02 §10 notification transport. One row per
-- browser registration; the runtime's notify.send executor fans out to all of
-- them (single user in v1). endpoint is unique so a re-subscribe upserts.
CREATE TABLE push_subscriptions (
  id         bigserial PRIMARY KEY,
  endpoint   text NOT NULL UNIQUE,
  p256dh     text NOT NULL,
  auth       text NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);
