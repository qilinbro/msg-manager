-- Rebuild the constrained identity graph atomically; preserve every existing
-- resource ID, message sequence and local management flag from schema v1.
CREATE TABLE email_v2_connections (
 id TEXT PRIMARY KEY,
 workspace_id TEXT NOT NULL CHECK(workspace_id='local-default') REFERENCES workspaces(id),
 platform TEXT NOT NULL CHECK(platform IN ('qq','weixin','telegram','shanliao','email')),
 account_id TEXT NOT NULL, display_name TEXT NOT NULL,
 source TEXT NOT NULL CHECK(source IN ('import','imap','mixed')),
 UNIQUE(workspace_id,platform,account_id)
);
CREATE TABLE email_v2_contacts (
 id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL CHECK(workspace_id='local-default') REFERENCES workspaces(id),
 connection_id TEXT NOT NULL REFERENCES email_v2_connections(id),
 peer_id TEXT NOT NULL, display_name TEXT NOT NULL, UNIQUE(connection_id,peer_id)
);
CREATE TABLE email_v2_conversations (
 id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL CHECK(workspace_id='local-default') REFERENCES workspaces(id),
 connection_id TEXT NOT NULL REFERENCES email_v2_connections(id),
 contact_id TEXT NOT NULL UNIQUE REFERENCES email_v2_contacts(id), peer_id TEXT NOT NULL, title TEXT NOT NULL,
 pinned INTEGER NOT NULL DEFAULT 0 CHECK(pinned IN (0,1)), archived INTEGER NOT NULL DEFAULT 0 CHECK(archived IN (0,1)),
 UNIQUE(connection_id,peer_id)
);
CREATE TABLE email_v2_messages (
 seq INTEGER PRIMARY KEY AUTOINCREMENT, id TEXT NOT NULL UNIQUE,
 workspace_id TEXT NOT NULL CHECK(workspace_id='local-default') REFERENCES workspaces(id),
 conversation_id TEXT NOT NULL REFERENCES email_v2_conversations(id), external_id TEXT NOT NULL,
 direction TEXT NOT NULL CHECK(direction IN ('incoming','outgoing')), body TEXT NOT NULL, occurred_at TEXT NOT NULL,
 is_read INTEGER NOT NULL CHECK(is_read IN (0,1)),
 source TEXT NOT NULL DEFAULT 'import' CHECK(source IN ('import','imap')),
 email_json TEXT, UNIQUE(conversation_id,external_id)
);
INSERT INTO email_v2_connections SELECT * FROM connections;
INSERT INTO email_v2_contacts SELECT * FROM contacts;
INSERT INTO email_v2_conversations SELECT * FROM conversations;
INSERT INTO email_v2_messages(seq,id,workspace_id,conversation_id,external_id,direction,body,occurred_at,is_read)
 SELECT seq,id,workspace_id,conversation_id,external_id,direction,body,occurred_at,is_read FROM messages;
DROP TRIGGER messages_fts_insert;
DROP TRIGGER messages_fts_delete;
DROP TRIGGER messages_fts_update;
DROP TABLE messages_fts;
DROP TABLE messages;
DROP TABLE conversations;
DROP TABLE contacts;
DROP TABLE connections;
ALTER TABLE email_v2_connections RENAME TO connections;
ALTER TABLE email_v2_contacts RENAME TO contacts;
ALTER TABLE email_v2_conversations RENAME TO conversations;
ALTER TABLE email_v2_messages RENAME TO messages;
CREATE INDEX messages_conversation_time ON messages(conversation_id,occurred_at DESC,seq DESC);
CREATE INDEX messages_unread ON messages(conversation_id,direction,is_read);
CREATE INDEX conversations_filters ON conversations(workspace_id,archived,pinned);
CREATE VIRTUAL TABLE messages_fts USING fts5(body,content='messages',content_rowid='seq');
CREATE TRIGGER messages_fts_insert AFTER INSERT ON messages BEGIN
 INSERT INTO messages_fts(rowid,body) VALUES(new.seq,new.body);
END;
CREATE TRIGGER messages_fts_delete AFTER DELETE ON messages BEGIN
 INSERT INTO messages_fts(messages_fts,rowid,body) VALUES('delete',old.seq,old.body);
END;
CREATE TRIGGER messages_fts_update AFTER UPDATE OF body ON messages BEGIN
 INSERT INTO messages_fts(messages_fts,rowid,body) VALUES('delete',old.seq,old.body);
 INSERT INTO messages_fts(rowid,body) VALUES(new.seq,new.body);
END;
INSERT INTO messages_fts(messages_fts) VALUES('rebuild');
UPDATE workspaces SET revision=revision+1;
