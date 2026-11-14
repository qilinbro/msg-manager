-- Only operator-selected private chat IDs and scheduling preferences live here.
-- This table is inside the encrypted product database; never store source paths
-- or database keys in monitor settings.
CREATE TABLE wechat_monitor_settings (
 workspace_id TEXT NOT NULL REFERENCES workspaces(id),
 account_id TEXT NOT NULL CHECK(length(account_id) BETWEEN 2 AND 128),
 enabled INTEGER NOT NULL CHECK(enabled IN (0,1)),
 peer_ids_json TEXT NOT NULL CHECK(length(peer_ids_json) BETWEEN 2 AND 2700),
 interval_seconds INTEGER NOT NULL CHECK(interval_seconds BETWEEN 10 AND 300),
 updated_at TEXT NOT NULL,
 PRIMARY KEY(workspace_id,account_id)
);
