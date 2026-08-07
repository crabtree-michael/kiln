-- Re-key the beta list on GitHub login. The list is now fed by the private-beta
-- gate rather than by a landing-page email form: someone who completes GitHub
-- auth but isn't on KILN_ALLOWED_GITHUB_USERS is recorded here so we have the
-- record that they tried to get in, and the only identifier that flow carries is
-- their login (GitHub's /user gives no email without a further scope).
--
-- The email column stays, nullable, because the rows already collected by the
-- old form are real interest we must not drop. Every row therefore carries an
-- email OR a login (the CHECK), never neither. Postgres allows repeated NULLs
-- under a UNIQUE constraint, so the historical email-only rows coexist with the
-- login-keyed ones without a partial index.
ALTER TABLE beta_signups ALTER COLUMN email DROP NOT NULL;
ALTER TABLE beta_signups ADD COLUMN github_login text;
ALTER TABLE beta_signups ADD CONSTRAINT beta_signups_github_login_key UNIQUE (github_login);
ALTER TABLE beta_signups ADD CONSTRAINT beta_signups_identified
  CHECK (email IS NOT NULL OR github_login IS NOT NULL);
