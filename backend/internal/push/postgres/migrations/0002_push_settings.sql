-- Global push-notification settings: docs/specs/02 §10. A single row (id = 1,
-- enforced by the CHECK) holds the notification frequency mode that gates when
-- the runtime emits a Web Push message. Single user in v1, so one global row
-- rather than a per-user setting. Defaults to 'blocked' — a push only when a
-- ticket needs a human decision, preserving the prior behavior.
CREATE TABLE push_settings (
  id   smallint PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  mode text NOT NULL DEFAULT 'blocked'
);

INSERT INTO push_settings (id, mode) VALUES (1, 'blocked');
