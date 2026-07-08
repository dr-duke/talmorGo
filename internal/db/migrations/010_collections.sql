CREATE TABLE IF NOT EXISTS collections (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE TABLE IF NOT EXISTS collection_jobs (
    collection_id TEXT NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    job_id        TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    added_at      TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    PRIMARY KEY (collection_id, job_id)
);
