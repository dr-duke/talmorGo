-- Трекинг пропавших файлов
ALTER TABLE files ADD COLUMN lost_at TEXT;

-- Теги (глобальные, назначаются заданиям)
CREATE TABLE IF NOT EXISTS tags (
    id   TEXT PRIMARY KEY,
    name TEXT NOT NULL UNIQUE
);

CREATE TABLE IF NOT EXISTS job_tags (
    job_id TEXT NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    tag_id TEXT NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (job_id, tag_id)
);
