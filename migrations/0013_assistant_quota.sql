-- Assistant calls have no conversation FK: global work must never invent a
-- contact or conversation just to consume the same persistent daily budget.
CREATE TABLE IF NOT EXISTS ai_assistant_call_reservations (
  id TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL CHECK(workspace_id='local-default'),
  purpose TEXT NOT NULL CHECK(purpose='assistant'),
  day TEXT NOT NULL CHECK(length(day)=10),
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS ai_assistant_daily_call_quota ON ai_assistant_call_reservations(workspace_id,day);
