-- The repo credential is now granted through the dashboard's repo-scoped
-- "Connect GitHub" OAuth flow, which replaced the manual token field
-- (integrations redesign). That flow writes the SAME github_auth_token_enc
-- column the manual field wrote to, so this migration deliberately does NOT
-- touch, move, or clear any stored token: an existing credential carries
-- forward untouched and keeps working, and there is never a second slot for a
-- legacy token to dangle in.
--
-- These two columns record what the connect flow additionally learns about the
-- credential. Both default to '' so every pre-existing row is valid without a
-- backfill — and '' on github_token_scopes means UNKNOWN, not "no scopes":
-- nobody recorded the scopes of a hand-typed PAT. The service reads unknown as
-- connected (the token is presumed good, which is why it was configured) and
-- only prompts a reconnect once GitHub has positively reported a scope list
-- that lacks `repo`. identity.Service.refreshGitHubScopes fills these in on the
-- next verify run, so the classification costs the user nothing.
ALTER TABLE user_config ADD COLUMN github_token_scopes text NOT NULL DEFAULT '';
ALTER TABLE user_config ADD COLUMN github_connected_login text NOT NULL DEFAULT '';
