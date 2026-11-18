-- Feishu is a narrowly scoped, opt-in private digest. No existing digest rows
-- or generic send/approval behavior are changed by this additive migration.
CREATE TABLE IF NOT EXISTS feishu_configs (
 workspace_id TEXT PRIMARY KEY REFERENCES workspaces(id),
 app_id TEXT NOT NULL DEFAULT '',
 app_secret TEXT NOT NULL DEFAULT '',
 recipient_open_id TEXT NOT NULL DEFAULT '',
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>=1)
);
INSERT OR IGNORE INTO feishu_configs(workspace_id) VALUES('local-default');
CREATE TABLE IF NOT EXISTS feishu_schedules (
 workspace_id TEXT PRIMARY KEY REFERENCES workspaces(id),
 enabled INTEGER NOT NULL DEFAULT 0 CHECK(enabled IN (0,1)),
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>=1),
 model_config_id TEXT NOT NULL DEFAULT '',
 model_config_revision INTEGER NOT NULL DEFAULT 0 CHECK(model_config_revision>=0),
 config_revision INTEGER NOT NULL DEFAULT 0 CHECK(config_revision>=0),
 target_binding_id TEXT NOT NULL DEFAULT '',
 next_window_end TEXT,
 last_window_end TEXT,
 authorized_at TEXT,
 authorized_by TEXT NOT NULL DEFAULT ''
);
INSERT OR IGNORE INTO feishu_schedules(workspace_id) VALUES('local-default');
CREATE TABLE IF NOT EXISTS feishu_reports (
 id TEXT PRIMARY KEY,
 workspace_id TEXT NOT NULL REFERENCES workspaces(id),
 window_start TEXT NOT NULL,
 window_end TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('running','succeeded','failed','unknown')),
 model_config_id TEXT NOT NULL,
 model_config_revision INTEGER NOT NULL CHECK(model_config_revision>=0),
 config_revision INTEGER NOT NULL CHECK(config_revision>=0),
 target_binding_id TEXT NOT NULL,
 schedule_revision INTEGER NOT NULL DEFAULT 0,
 trigger TEXT NOT NULL CHECK(trigger IN ('manual','schedule')),
 summary TEXT NOT NULL DEFAULT '',
 todos_json TEXT NOT NULL DEFAULT '[]',
 excerpts_json TEXT NOT NULL DEFAULT '[]',
 payload_text TEXT NOT NULL DEFAULT '',
 content_hash TEXT NOT NULL DEFAULT '',
 truncated INTEGER NOT NULL DEFAULT 0 CHECK(truncated IN (0,1)),
 error_code TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL,
 generated_at TEXT,
 UNIQUE(workspace_id,window_end)
);
-- Deliberately no cascading conversation foreign key: a missing/deleted source
-- must fail privacy validation, not erase provenance and expose derived text.
CREATE TABLE IF NOT EXISTS feishu_report_sources (
 workspace_id TEXT NOT NULL,
 report_id TEXT NOT NULL REFERENCES feishu_reports(id) ON DELETE CASCADE,
 conversation_id TEXT NOT NULL,
 settings_revision INTEGER NOT NULL CHECK(settings_revision>=0),
 PRIMARY KEY(workspace_id,report_id,conversation_id)
);
CREATE TABLE IF NOT EXISTS feishu_deliveries (
 id TEXT PRIMARY KEY,
 workspace_id TEXT NOT NULL REFERENCES workspaces(id),
 report_id TEXT NOT NULL,
 window_start TEXT NOT NULL,
 window_end TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('running','succeeded','failed','unknown')),
 model_config_id TEXT NOT NULL,
 model_config_revision INTEGER NOT NULL CHECK(model_config_revision>=0),
 config_revision INTEGER NOT NULL CHECK(config_revision>=0),
 target_binding_id TEXT NOT NULL,
 content_hash TEXT NOT NULL,
 message_id TEXT NOT NULL DEFAULT '',
 error_code TEXT NOT NULL DEFAULT '',
 created_at TEXT NOT NULL,
 completed_at TEXT,
 UNIQUE(workspace_id,window_end),
 UNIQUE(workspace_id,report_id)
);
CREATE TABLE IF NOT EXISTS ai_feishu_call_reservations (
 id TEXT PRIMARY KEY,
 workspace_id TEXT NOT NULL REFERENCES workspaces(id),
 purpose TEXT NOT NULL DEFAULT 'feishu_digest' CHECK(purpose='feishu_digest'),
 day TEXT NOT NULL,
 created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS ai_feishu_calls_day ON ai_feishu_call_reservations(workspace_id,day);
