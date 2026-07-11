CREATE TABLE IF NOT EXISTS operations (
    id          TEXT PRIMARY KEY,
    kind        TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'pending',
    title       TEXT NOT NULL,
    payload     TEXT NOT NULL DEFAULT '{}',
    created_at  TEXT NOT NULL,
    started_at  TEXT,
    finished_at TEXT,
    error       TEXT
);
