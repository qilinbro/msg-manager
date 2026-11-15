CREATE TABLE contact_profiles (
 conversation_id TEXT PRIMARY KEY,
 workspace_id TEXT NOT NULL,connection_id TEXT NOT NULL,contact_id TEXT NOT NULL,
 public_name TEXT NOT NULL DEFAULT '',company TEXT NOT NULL DEFAULT '',role TEXT NOT NULL DEFAULT '',manual_notes TEXT NOT NULL DEFAULT '',
 category_override TEXT NOT NULL DEFAULT '' CHECK(category_override IN ('','unclassified','friend','family','colleague','client','partner','other')),
 auto_organize INTEGER NOT NULL DEFAULT 0 CHECK(auto_organize IN (0,1)),
 public_search_enabled INTEGER NOT NULL DEFAULT 0 CHECK(public_search_enabled IN (0,1)),
 model_config_id TEXT NOT NULL DEFAULT '',revision INTEGER NOT NULL DEFAULT 0 CHECK(revision>=0),
 processed_seq INTEGER NOT NULL DEFAULT 0 CHECK(processed_seq>=0),
 ai_category TEXT NOT NULL DEFAULT 'unclassified' CHECK(ai_category IN ('unclassified','friend','family','colleague','client','partner','other')),
 tags_json TEXT NOT NULL DEFAULT '[]',summary TEXT NOT NULL DEFAULT '',evidence_json TEXT NOT NULL DEFAULT '[]',
 document_truncated INTEGER NOT NULL DEFAULT 0 CHECK(document_truncated IN (0,1)),last_enrich_day TEXT NOT NULL DEFAULT '',
 updated_at TEXT NOT NULL,
 UNIQUE(workspace_id,conversation_id),
 FOREIGN KEY(workspace_id,connection_id,contact_id,conversation_id) REFERENCES conversations(workspace_id,connection_id,contact_id,id)
);
CREATE TABLE profile_sources (
 id TEXT PRIMARY KEY,workspace_id TEXT NOT NULL,conversation_id TEXT NOT NULL,
 url TEXT NOT NULL,title TEXT NOT NULL,snippet TEXT NOT NULL,content_hash TEXT NOT NULL,retrieved_at TEXT NOT NULL,
 UNIQUE(workspace_id,conversation_id,id),
 FOREIGN KEY(workspace_id,conversation_id) REFERENCES contact_profiles(workspace_id,conversation_id) ON DELETE CASCADE
);
CREATE TABLE profile_facts (
 id TEXT PRIMARY KEY,workspace_id TEXT NOT NULL,conversation_id TEXT NOT NULL,
 field TEXT NOT NULL CHECK(field IN ('name','company','role','interest','project','preference','public_bio')),
 value TEXT NOT NULL,state TEXT NOT NULL CHECK(state IN ('unverified','confirmed','rejected')),
 origin TEXT NOT NULL CHECK(origin IN ('chat','public')),
 created_at TEXT NOT NULL,updated_at TEXT NOT NULL,
 UNIQUE(workspace_id,conversation_id,id),
 FOREIGN KEY(workspace_id,conversation_id) REFERENCES contact_profiles(workspace_id,conversation_id) ON DELETE CASCADE
);
CREATE TABLE profile_fact_messages (
 workspace_id TEXT NOT NULL,conversation_id TEXT NOT NULL,fact_id TEXT NOT NULL,message_id TEXT NOT NULL,
 PRIMARY KEY(fact_id,message_id),
 FOREIGN KEY(workspace_id,conversation_id,fact_id) REFERENCES profile_facts(workspace_id,conversation_id,id) ON DELETE CASCADE,
 FOREIGN KEY(workspace_id,conversation_id,message_id) REFERENCES messages(workspace_id,conversation_id,id)
);
CREATE TABLE profile_fact_sources (
 workspace_id TEXT NOT NULL,conversation_id TEXT NOT NULL,fact_id TEXT NOT NULL,source_id TEXT NOT NULL,
 PRIMARY KEY(fact_id,source_id),
 FOREIGN KEY(workspace_id,conversation_id,fact_id) REFERENCES profile_facts(workspace_id,conversation_id,id) ON DELETE CASCADE,
 FOREIGN KEY(workspace_id,conversation_id,source_id) REFERENCES profile_sources(workspace_id,conversation_id,id)
);
CREATE TABLE profile_jobs (
 id TEXT PRIMARY KEY,workspace_id TEXT NOT NULL,conversation_id TEXT NOT NULL,
 kind TEXT NOT NULL CHECK(kind IN ('organize','enrich')),
 state TEXT NOT NULL CHECK(state IN ('queued','running','succeeded','failed','cancelled')),
 attempts INTEGER NOT NULL DEFAULT 0 CHECK(attempts>=0 AND attempts<=3),
 next_attempt_at TEXT,lease_until TEXT,lease_token TEXT NOT NULL DEFAULT '',fence INTEGER NOT NULL DEFAULT 0,
 model_config_id TEXT NOT NULL,settings_revision INTEGER NOT NULL,max_seq INTEGER NOT NULL,
 input_hash TEXT NOT NULL DEFAULT '',last_error_code TEXT,updated_at TEXT NOT NULL,
 manual INTEGER NOT NULL DEFAULT 0 CHECK(manual IN (0,1)),
 UNIQUE(workspace_id,conversation_id,kind),
 FOREIGN KEY(workspace_id,conversation_id) REFERENCES contact_profiles(workspace_id,conversation_id) ON DELETE CASCADE
);
CREATE INDEX profile_jobs_ready ON profile_jobs(workspace_id,model_config_id,state,next_attempt_at,lease_until);
CREATE INDEX profile_facts_visible ON profile_facts(workspace_id,conversation_id,state,updated_at DESC,id);
CREATE TABLE profile_search_reservations (
 id TEXT PRIMARY KEY,workspace_id TEXT NOT NULL,conversation_id TEXT NOT NULL,
 day TEXT NOT NULL CHECK(length(day)=10),created_at TEXT NOT NULL,
 FOREIGN KEY(workspace_id,conversation_id) REFERENCES contact_profiles(workspace_id,conversation_id)
);
CREATE INDEX profile_search_daily ON profile_search_reservations(workspace_id,day);
