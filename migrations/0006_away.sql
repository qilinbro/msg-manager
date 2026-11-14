CREATE TABLE away_sessions (
 id TEXT PRIMARY KEY,
 status TEXT NOT NULL CHECK(status IN ('running','stopped','ended')),
 started_ms INTEGER NOT NULL,
 ends_ms INTEGER NOT NULL,
 stopped_ms INTEGER NOT NULL DEFAULT 0,
 sender TEXT NOT NULL,
 account_key TEXT NOT NULL,
 model_key TEXT NOT NULL,
 uid_validity INTEGER NOT NULL,
 next_uid INTEGER NOT NULL
);
CREATE UNIQUE INDEX one_active_away_session ON away_sessions(status) WHERE status='running';
CREATE TABLE away_tasks (
 id TEXT PRIMARY KEY REFERENCES messages(id),
 session_id TEXT NOT NULL REFERENCES away_sessions(id),
 received_ms INTEGER NOT NULL,
 title TEXT NOT NULL,
 requirements TEXT NOT NULL DEFAULT '[]',
 deadline TEXT NOT NULL DEFAULT '',
 evidence TEXT NOT NULL DEFAULT '',
 draft TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL CHECK(state IN ('queued','processing','ready','failed')),
 done INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX away_tasks_by_session ON away_tasks(session_id, received_ms);
CREATE TABLE away_events (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 session_id TEXT NOT NULL REFERENCES away_sessions(id),
 at_ms INTEGER NOT NULL,
 kind TEXT NOT NULL,
 detail TEXT NOT NULL
);
CREATE INDEX away_events_by_session ON away_events(session_id, seq);
