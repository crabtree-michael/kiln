-- Beta-signup emails collected by the landing page's "Join the beta" form. One
-- row per interested visitor; email is unique so a repeat submit upserts to a
-- no-op rather than duplicating. There is no read side in v1 — the list is
-- inspected out-of-band.
CREATE TABLE beta_signups (
  id         bigserial PRIMARY KEY,
  email      text NOT NULL UNIQUE,
  created_at timestamptz NOT NULL DEFAULT now()
);
