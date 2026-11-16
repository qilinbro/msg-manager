-- Persist only the operator-selected QQ Milky private-chat allowlist.
-- Protocol credentials and endpoint configuration remain process-local.
CREATE TABLE qq_milky_monitor_settings (
 workspace_id TEXT NOT NULL REFERENCES workspaces(id),
 account_id TEXT NOT NULL CHECK(length(account_id) BETWEEN 2 AND 20),
 enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
 peer_ids_json TEXT NOT NULL CHECK(length(peer_ids_json) BETWEEN 2 AND 500),
 updated_at TEXT NOT NULL,
 PRIMARY KEY(workspace_id,account_id)
);
