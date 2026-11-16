CREATE TABLE digest_schedules (
 workspace_id TEXT PRIMARY KEY REFERENCES workspaces(id),
 enabled INTEGER NOT NULL DEFAULT 0 CHECK(enabled IN (0,1)),
 local_time TEXT NOT NULL DEFAULT '08:00' CHECK(length(local_time)=5),
 timezone TEXT NOT NULL DEFAULT 'Asia/Shanghai' CHECK(timezone='Asia/Shanghai'),
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>=1),
 last_run_day TEXT NOT NULL DEFAULT ''
);
INSERT INTO digest_schedules(workspace_id) VALUES ('local-default');

CREATE TABLE digest_reports (
 id TEXT PRIMARY KEY,
 workspace_id TEXT NOT NULL REFERENCES workspaces(id),
 day TEXT NOT NULL CHECK(length(day)=10),
 state TEXT NOT NULL CHECK(state IN ('queued','running','succeeded','failed','unknown')),
 summary TEXT NOT NULL DEFAULT '',
 reminders_json TEXT NOT NULL DEFAULT '[]',
 evidence_json TEXT NOT NULL DEFAULT '[]',
 conversation_count INTEGER NOT NULL DEFAULT 0 CHECK(conversation_count>=0),
 error_code TEXT,
 generated_at TEXT,
 created_at TEXT NOT NULL,
 UNIQUE(workspace_id,day)
);
CREATE INDEX digest_reports_recent ON digest_reports(workspace_id,day DESC);
