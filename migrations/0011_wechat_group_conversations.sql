ALTER TABLE conversations ADD COLUMN conversation_kind TEXT NOT NULL DEFAULT 'private' CHECK(conversation_kind IN ('private','group'));
CREATE INDEX conversations_kind ON conversations(workspace_id,conversation_kind,archived);
