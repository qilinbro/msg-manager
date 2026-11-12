CREATE TABLE workspaces (
    id TEXT PRIMARY KEY CHECK (id = 'local-default'),
    revision INTEGER NOT NULL DEFAULT 0
);
INSERT INTO workspaces(id) VALUES ('local-default');

CREATE TABLE connections (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL CHECK (workspace_id = 'local-default') REFERENCES workspaces(id),
    platform TEXT NOT NULL CHECK (platform IN ('qq','weixin','telegram','shanliao')),
    account_id TEXT NOT NULL,
    display_name TEXT NOT NULL,
    source TEXT NOT NULL CHECK (source = 'import'),
    UNIQUE (workspace_id, platform, account_id)
);

CREATE TABLE contacts (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL CHECK (workspace_id = 'local-default') REFERENCES workspaces(id),
    connection_id TEXT NOT NULL REFERENCES connections(id),
    peer_id TEXT NOT NULL,
    display_name TEXT NOT NULL,
    UNIQUE (connection_id, peer_id)
);

CREATE TABLE conversations (
    id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL CHECK (workspace_id = 'local-default') REFERENCES workspaces(id),
    connection_id TEXT NOT NULL REFERENCES connections(id),
    contact_id TEXT NOT NULL UNIQUE REFERENCES contacts(id),
    peer_id TEXT NOT NULL,
    title TEXT NOT NULL,
    pinned INTEGER NOT NULL DEFAULT 0 CHECK (pinned IN (0,1)),
    archived INTEGER NOT NULL DEFAULT 0 CHECK (archived IN (0,1)),
    UNIQUE (connection_id, peer_id)
);

CREATE TABLE messages (
    seq INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL UNIQUE,
    workspace_id TEXT NOT NULL CHECK (workspace_id = 'local-default') REFERENCES workspaces(id),
    conversation_id TEXT NOT NULL REFERENCES conversations(id),
    external_id TEXT NOT NULL,
    direction TEXT NOT NULL CHECK (direction IN ('incoming','outgoing')),
    body TEXT NOT NULL,
    occurred_at TEXT NOT NULL,
    is_read INTEGER NOT NULL CHECK (is_read IN (0,1)),
    UNIQUE (conversation_id, external_id)
);
CREATE INDEX messages_conversation_time ON messages(conversation_id, occurred_at DESC, seq DESC);
CREATE INDEX messages_unread ON messages(conversation_id, direction, is_read);
CREATE INDEX conversations_filters ON conversations(workspace_id, archived, pinned);
CREATE VIRTUAL TABLE messages_fts USING fts5(body, content='messages', content_rowid='seq');
CREATE TRIGGER messages_fts_insert AFTER INSERT ON messages BEGIN
    INSERT INTO messages_fts(rowid, body) VALUES (new.seq, new.body);
END;
CREATE TRIGGER messages_fts_delete AFTER DELETE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, body) VALUES ('delete', old.seq, old.body);
END;
CREATE TRIGGER messages_fts_update AFTER UPDATE OF body ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, body) VALUES ('delete', old.seq, old.body);
    INSERT INTO messages_fts(rowid, body) VALUES (new.seq, new.body);
END;
