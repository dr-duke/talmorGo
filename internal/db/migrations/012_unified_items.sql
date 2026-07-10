-- Migration 012: унификация медиаэлементов.
-- files + audio_files → items(kind).
-- Данные дропаются намеренно (восстанавливаются через повторную загрузку).

-- Удаляем старые таблицы.
DROP TABLE IF EXISTS audio_files;
DROP TABLE IF EXISTS tokens;   -- пересоздаём с item_id
DROP TABLE IF EXISTS files;    -- заменяем на items

-- Удаляем file_id из jobs (связь теперь обратная: items.job_id → jobs.id).
ALTER TABLE jobs DROP COLUMN file_id;

-- Основная таблица медиаэлементов (видео + аудио).
CREATE TABLE IF NOT EXISTS items (
    id         TEXT PRIMARY KEY,
    job_id     TEXT REFERENCES jobs(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL DEFAULT 'video' CHECK (kind IN ('video','audio')),
    path       TEXT NOT NULL UNIQUE,
    name       TEXT NOT NULL,
    size       INTEGER NOT NULL DEFAULT 0,
    duration   INTEGER NOT NULL DEFAULT 0,
    title      TEXT NOT NULL DEFAULT '',
    artist     TEXT NOT NULL DEFAULT '',
    album      TEXT NOT NULL DEFAULT '',
    year       TEXT NOT NULL DEFAULT '',
    genre      TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')),
    deleted_at TEXT,
    lost_at    TEXT
);

CREATE INDEX IF NOT EXISTS idx_items_job_id     ON items(job_id);
CREATE INDEX IF NOT EXISTS idx_items_kind       ON items(kind);
CREATE INDEX IF NOT EXISTS idx_items_created_at ON items(created_at);

-- Presigned-токены на items.
CREATE TABLE IF NOT EXISTS tokens (
    token      TEXT PRIMARY KEY,
    item_id    TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);

CREATE INDEX IF NOT EXISTS idx_tokens_item ON tokens(item_id);

-- Расширяем теги: kind = 'plain' | 'collection'.
ALTER TABLE tags ADD COLUMN kind TEXT NOT NULL DEFAULT 'plain';

-- Коллекционные теги маркируем.
UPDATE tags SET kind = 'collection'
WHERE name IN (SELECT name FROM collections);
