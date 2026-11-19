-- Narrow Bot API receive provenance. This does not turn the generic ingestion
-- scaffold into a completed pipeline and never persists raw unsupported data.
CREATE TABLE IF NOT EXISTS telegram_bot_cursors (
 workspace_id TEXT NOT NULL CHECK(workspace_id='local-default') REFERENCES workspaces(id),
 bot_id TEXT NOT NULL,
 next_offset TEXT NOT NULL DEFAULT '0',
 revision INTEGER NOT NULL DEFAULT 0 CHECK(revision>=0),
 epoch INTEGER NOT NULL DEFAULT 0 CHECK(epoch>=0),
 last_event_at TEXT NOT NULL DEFAULT '',
 updated_at TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(workspace_id,bot_id)
);
CREATE TABLE IF NOT EXISTS telegram_bot_messages (
 message_id TEXT PRIMARY KEY REFERENCES messages(id),
 workspace_id TEXT NOT NULL CHECK(workspace_id='local-default') REFERENCES workspaces(id),
 connection_id TEXT NOT NULL REFERENCES connections(id),
 bot_id TEXT NOT NULL,
 chat_id TEXT NOT NULL,
 external_message_id TEXT NOT NULL,
 UNIQUE(workspace_id,bot_id,chat_id,external_message_id),
 FOREIGN KEY(workspace_id,bot_id) REFERENCES telegram_bot_cursors(workspace_id,bot_id)
);
CREATE INDEX IF NOT EXISTS telegram_bot_messages_connection ON telegram_bot_messages(connection_id,message_id);
-- update_id may restart randomly after a week without updates. Event identity
-- is scoped to a receive epoch; private message identity stays stable across it.
CREATE TABLE IF NOT EXISTS telegram_bot_events (
 workspace_id TEXT NOT NULL CHECK(workspace_id='local-default') REFERENCES workspaces(id),
 bot_id TEXT NOT NULL,
 epoch INTEGER NOT NULL CHECK(epoch>=0),
 update_id TEXT NOT NULL,
 event_type TEXT NOT NULL,
 reason TEXT NOT NULL CHECK(reason IN ('','not_private','not_authorized','unsupported_media','unsupported_message','malformed_message','unsupported_edit','unsupported_event')),
 message_id TEXT REFERENCES telegram_bot_messages(message_id),
 event_digest TEXT NOT NULL CHECK(length(event_digest)=64),
 received_at TEXT NOT NULL,
 PRIMARY KEY(workspace_id,bot_id,epoch,update_id),
 FOREIGN KEY(workspace_id,bot_id) REFERENCES telegram_bot_cursors(workspace_id,bot_id),
 CHECK((reason='' AND message_id IS NOT NULL) OR (reason<>'' AND message_id IS NULL))
);
CREATE INDEX IF NOT EXISTS telegram_bot_events_message ON telegram_bot_events(message_id);
