CREATE UNIQUE INDEX ai_conversation_scope ON conversations(workspace_id,connection_id,contact_id,id);
CREATE UNIQUE INDEX ai_message_scope ON messages(workspace_id,conversation_id,id);

CREATE TABLE ai_preferences (
 conversation_id TEXT PRIMARY KEY,
 workspace_id TEXT NOT NULL CHECK(workspace_id='local-default'),
 connection_id TEXT NOT NULL,contact_id TEXT NOT NULL,
 mode TEXT NOT NULL DEFAULT 'draft_only' CHECK(mode IN ('draft_only','paused')),
 tone TEXT NOT NULL DEFAULT 'natural' CHECK(tone IN ('natural','casual','professional')),
 revision INTEGER NOT NULL DEFAULT 0 CHECK(revision>=0),
 FOREIGN KEY(workspace_id,connection_id,contact_id,conversation_id) REFERENCES conversations(workspace_id,connection_id,contact_id,id)
);

CREATE TABLE ai_memories (
 id TEXT PRIMARY KEY,
 workspace_id TEXT NOT NULL CHECK(workspace_id='local-default'),
 connection_id TEXT NOT NULL,contact_id TEXT NOT NULL,conversation_id TEXT NOT NULL,
 content TEXT NOT NULL,
 verification_state TEXT NOT NULL CHECK(verification_state IN ('unverified','confirmed')),
 created_at TEXT NOT NULL,updated_at TEXT NOT NULL,
 UNIQUE(workspace_id,conversation_id,id),
 FOREIGN KEY(workspace_id,connection_id,contact_id,conversation_id) REFERENCES conversations(workspace_id,connection_id,contact_id,id)
);
CREATE INDEX ai_memories_scope ON ai_memories(workspace_id,conversation_id,verification_state,updated_at DESC,id DESC);
CREATE TABLE ai_memory_evidence (
 memory_id TEXT NOT NULL,message_id TEXT NOT NULL,
 workspace_id TEXT NOT NULL CHECK(workspace_id='local-default'),conversation_id TEXT NOT NULL,
 PRIMARY KEY(memory_id,message_id),
 FOREIGN KEY(workspace_id,conversation_id,memory_id) REFERENCES ai_memories(workspace_id,conversation_id,id) ON DELETE CASCADE,
 FOREIGN KEY(workspace_id,conversation_id,message_id) REFERENCES messages(workspace_id,conversation_id,id)
);

CREATE TABLE ai_analyses (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 id TEXT NOT NULL UNIQUE,
 workspace_id TEXT NOT NULL CHECK(workspace_id='local-default'),
 connection_id TEXT NOT NULL,contact_id TEXT NOT NULL,conversation_id TEXT NOT NULL,
 context_hash TEXT NOT NULL CHECK(length(context_hash)=64),
 result_json TEXT NOT NULL,draft_body TEXT NOT NULL,
 draft_edited INTEGER NOT NULL DEFAULT 0 CHECK(draft_edited IN (0,1)),
 created_at TEXT NOT NULL,updated_at TEXT NOT NULL,
 UNIQUE(workspace_id,conversation_id,id),
 FOREIGN KEY(workspace_id,connection_id,contact_id,conversation_id) REFERENCES conversations(workspace_id,connection_id,contact_id,id)
);
CREATE INDEX ai_analyses_latest ON ai_analyses(workspace_id,conversation_id,seq DESC);
CREATE TABLE ai_analysis_evidence (
 analysis_id TEXT NOT NULL,message_id TEXT NOT NULL,
 workspace_id TEXT NOT NULL CHECK(workspace_id='local-default'),conversation_id TEXT NOT NULL,
 PRIMARY KEY(analysis_id,message_id),
 FOREIGN KEY(workspace_id,conversation_id,analysis_id) REFERENCES ai_analyses(workspace_id,conversation_id,id) ON DELETE CASCADE,
 FOREIGN KEY(workspace_id,conversation_id,message_id) REFERENCES messages(workspace_id,conversation_id,id)
);

CREATE TABLE ai_call_reservations (
 id TEXT PRIMARY KEY,
 workspace_id TEXT NOT NULL CHECK(workspace_id='local-default'),
 conversation_id TEXT NOT NULL REFERENCES conversations(id),
 day TEXT NOT NULL CHECK(length(day)=10),created_at TEXT NOT NULL
);
CREATE INDEX ai_daily_call_quota ON ai_call_reservations(workspace_id,day);
