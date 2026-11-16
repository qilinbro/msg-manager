-- Model credentials are confined to the existing SQLCipher database.
CREATE TABLE ai_model_config (
 workspace_id TEXT PRIMARY KEY REFERENCES workspaces(id),
 base_url TEXT NOT NULL CHECK(length(base_url) BETWEEN 1 AND 2048),
 model TEXT NOT NULL CHECK(length(model) BETWEEN 1 AND 128),
 api_key TEXT NOT NULL CHECK(length(api_key)<=4096),
 daily_limit INTEGER NOT NULL CHECK(daily_limit BETWEEN 1 AND 1000),
 revision INTEGER NOT NULL CHECK(revision>=1)
);
