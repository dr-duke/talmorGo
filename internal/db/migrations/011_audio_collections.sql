-- Коллекции теперь трекаются через job_tags (имя коллекции = имя тега).
-- Удаляем устаревшую pivot-таблицу.
DROP TABLE IF EXISTS collection_jobs;

-- Аудиофайлы, извлечённые из видео через ffmpeg.
CREATE TABLE IF NOT EXISTS audio_files (
    id         TEXT PRIMARY KEY,
    job_id     TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    file_id    TEXT REFERENCES files(id) ON DELETE SET NULL,
    path       TEXT NOT NULL UNIQUE,
    name       TEXT NOT NULL,
    size       INTEGER NOT NULL DEFAULT 0,
    title      TEXT NOT NULL DEFAULT '',
    artist     TEXT NOT NULL DEFAULT '',
    album      TEXT NOT NULL DEFAULT '',
    year       TEXT NOT NULL DEFAULT '',
    genre      TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
