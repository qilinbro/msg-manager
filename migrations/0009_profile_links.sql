-- User-confirmed, reversible profile associations. Original conversations,
-- messages, evidence and per-conversation AI permissions remain independent.
CREATE TABLE profile_links (
 workspace_id TEXT NOT NULL CHECK(workspace_id='local-default'),
 left_conversation_id TEXT NOT NULL,
 right_conversation_id TEXT NOT NULL,
 created_at TEXT NOT NULL,
 PRIMARY KEY(workspace_id,left_conversation_id,right_conversation_id),
 CHECK(left_conversation_id < right_conversation_id),
 FOREIGN KEY(workspace_id,left_conversation_id) REFERENCES contact_profiles(workspace_id,conversation_id) ON DELETE CASCADE,
 FOREIGN KEY(workspace_id,right_conversation_id) REFERENCES contact_profiles(workspace_id,conversation_id) ON DELETE CASCADE
);
CREATE INDEX profile_links_reverse ON profile_links(workspace_id,right_conversation_id,left_conversation_id);
