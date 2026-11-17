-- Protocol credentials are saved only inside the already encrypted SQLCipher
-- message database; no plaintext configuration file or browser secret is used.
CREATE TABLE IF NOT EXISTS qq_milky_connection_config (
 workspace_id TEXT PRIMARY KEY REFERENCES workspaces(id),
 url TEXT NOT NULL CHECK(length(url) BETWEEN 1 AND 4096),
 account_id TEXT NOT NULL CHECK(length(account_id) BETWEEN 2 AND 20),
 access_token TEXT NOT NULL CHECK(length(access_token) BETWEEN 1 AND 4096),
 history_limit INTEGER NOT NULL CHECK(history_limit BETWEEN 1 AND 30),
 revision INTEGER NOT NULL CHECK(revision > 0)
);
