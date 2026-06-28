CREATE TABLE IF NOT EXISTS cookies (
    domain     TEXT PRIMARY KEY,
    content    TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
