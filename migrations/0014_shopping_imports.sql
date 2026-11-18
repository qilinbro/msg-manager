-- Run only by migrateShoppingConnections on a pinned connection with foreign
-- keys disabled before BEGIN. Preserve parent IDs; do not rebuild children,
-- messages, FTS or the AI/profile evidence graph. Check all FKs before COMMIT.
CREATE TABLE shopping_v14_connections (
 id TEXT PRIMARY KEY,
 workspace_id TEXT NOT NULL CHECK(workspace_id='local-default') REFERENCES workspaces(id),
 platform TEXT NOT NULL CHECK(platform IN ('qq','weixin','telegram','shanliao','email','xianyu','taobao','jd','pinduoduo')),
 account_id TEXT NOT NULL,
 display_name TEXT NOT NULL,
 source TEXT NOT NULL CHECK(source IN ('import','imap','mixed')),
 connector_source TEXT CHECK(connector_source IS NULL OR (platform='weixin' AND connector_source IN ('wechatdb','mixed'))),
 UNIQUE(workspace_id,platform,account_id),
 CHECK(platform NOT IN ('xianyu','taobao','jd','pinduoduo') OR (source='import' AND connector_source IS NULL))
);
INSERT INTO shopping_v14_connections(id,workspace_id,platform,account_id,display_name,source,connector_source)
 SELECT id,workspace_id,platform,account_id,display_name,source,connector_source FROM connections;
DROP TABLE connections;
ALTER TABLE shopping_v14_connections RENAME TO connections;
