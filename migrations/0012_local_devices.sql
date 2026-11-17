-- Local grants contain only a canonical public P-256 SPKI and bound metadata.
-- No master/access token or private key is persisted by device authorization.
CREATE TABLE IF NOT EXISTS local_devices (
 workspace_id TEXT NOT NULL CHECK(workspace_id='local-default') REFERENCES workspaces(id),
 id TEXT NOT NULL CHECK(length(id)=22 AND id NOT GLOB '*[^A-Za-z0-9_-]*' AND substr(id,22,1) IN ('A','Q','g','w')),
 public_key BLOB NOT NULL CHECK(typeof(public_key)='blob' AND length(public_key)=91),
 service_origin TEXT NOT NULL CHECK(service_origin='http://127.0.0.1:' || CAST(CAST(substr(service_origin,18) AS INTEGER) AS TEXT) AND CAST(substr(service_origin,18) AS INTEGER) BETWEEN 1 AND 65535),
 frontend_origin TEXT NOT NULL CHECK(frontend_origin IN ('http://127.0.0.1:1420','http://localhost:1420','tauri://localhost','http://tauri.localhost','https://tauri.localhost')),
 issuer_hash TEXT NOT NULL CHECK(length(issuer_hash)=64 AND issuer_hash NOT GLOB '*[^0-9a-f]*'),
 created_at INTEGER NOT NULL CHECK(typeof(created_at)='integer' AND created_at BETWEEN 1 AND 253402300799),
 expires_at INTEGER NOT NULL CHECK(typeof(expires_at)='integer' AND expires_at>created_at AND expires_at<=253402300799 AND expires_at-created_at<=7776000),
 PRIMARY KEY(workspace_id,id)
);
CREATE INDEX IF NOT EXISTS local_devices_expiry ON local_devices(workspace_id,expires_at);
