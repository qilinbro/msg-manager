CREATE TABLE receive_monitors (
    workspace_id TEXT NOT NULL CHECK(workspace_id='local-default') REFERENCES workspaces(id),
    scope TEXT NOT NULL CHECK(scope IN ('email','telegram')),
    binding TEXT NOT NULL CHECK(length(binding) BETWEEN 1 AND 128),
    state_json TEXT NOT NULL CHECK(length(state_json) BETWEEN 2 AND 16384),
    updated_at TEXT NOT NULL,
    PRIMARY KEY(workspace_id,scope,binding)
);
