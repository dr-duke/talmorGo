ALTER TABLE files ADD COLUMN job_id TEXT REFERENCES jobs(id) ON DELETE SET NULL;

-- Бэкфилл: привязываем существующие файлы к их заданиям через jobs.file_id
UPDATE files
SET job_id = (SELECT id FROM jobs WHERE file_id = files.id LIMIT 1)
WHERE job_id IS NULL;
