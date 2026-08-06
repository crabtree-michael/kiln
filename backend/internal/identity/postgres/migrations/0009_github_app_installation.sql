-- GitHub OAuth App -> GitHub App (design 2026-08-04; migration decided
-- 2026-08-06: log every existing user out rather than carry the old credential
-- forward).
--
-- What changes in the credential model: an OAuth App hands over a long-lived,
-- account-wide token that Kiln must store forever. A GitHub App hands over an
-- INSTALLATION — the repositories the user picked on GitHub's own chooser — and
-- Kiln mints an hour-long token against it, on demand, with the App private key
-- that never leaves the backend. So the stored secret becomes a stored
-- identifier, and the blast radius of the database shrinks accordingly.
ALTER TABLE user_config
  ADD COLUMN github_installation_id bigint NOT NULL DEFAULT 0;

-- Set when GitHub rejects a mint (uninstalled, suspended, access withdrawn), so
-- the Integrations card can prompt a re-install. It is a RECORD of what the
-- credential path already learned, which is what keeps GET /api/me a pure DB
-- read — re-deriving it would make every dashboard render a GitHub round trip.
-- Cleared when the user comes back through the install flow.
ALTER TABLE user_config
  ADD COLUMN github_installation_revoked_at timestamptz;

-- Finding "every user on this installation" is the one lookup the credential
-- path does by something other than user_id: a mint fails knowing only the
-- installation it failed against.
CREATE INDEX idx_user_config_github_installation
  ON user_config (github_installation_id)
  WHERE github_installation_id <> 0;

-- github_token_scopes went with the OAuth App. A GitHub App has no per-grant
-- scope list — its reach is the permissions set at registration and the
-- repositories chosen at installation — so there is nothing left for this
-- column to hold. (It was never written anyway: the upsert added in 0008 never
-- listed it.)
--
-- IF EXISTS because a database predating 0008 never had the column, and this
-- migration has nothing to say to it either way.
ALTER TABLE user_config DROP COLUMN IF EXISTS github_token_scopes;

-- Log everyone out. Sessions themselves are not GitHub-specific, but the
-- credential behind every existing one is: each was minted by an OAuth grant
-- whose token this migration is about to delete. Leaving the sessions alive
-- would leave people signed in to an account with no repository access and no
-- prompt to fix it — the sign-in flow IS the connect flow, so the honest way
-- back is through the front door.
DELETE FROM sessions;

-- Drop the OAuth-era credential itself. Every one of these tokens was granted
-- to an OAuth App that this deploy stops using; keeping them would mean a
-- silently-unconnected user reading as "connected" off a credential nothing
-- mints against any more. Users sign in again and land on GitHub's repository
-- chooser — which is the point of the whole change, and not something a
-- backfill could have done for them.
--
-- This also clears hand-typed PATs, which share the column. That is a real
-- cost, accepted knowingly: there is no way to tell a PAT from an OAuth token
-- in the stored ciphertext, and leaving a credential of unknown provenance in
-- place would defeat the log-everyone-out decision. A PAT can be re-entered
-- through PUT /api/settings, and the deployment's bootstrap GITHUB_AUTH_TOKEN
-- re-seeds itself on the next boot.
UPDATE user_config
   SET github_auth_token_enc  = NULL,
       github_connected_login = '',
       updated_at             = now()
 WHERE github_auth_token_enc IS NOT NULL;
