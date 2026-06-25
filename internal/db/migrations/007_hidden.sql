-- Скрытые записи (missing/deleted → повторное нажатие delete скрывает строку)
ALTER TABLE jobs ADD COLUMN hidden INTEGER NOT NULL DEFAULT 0;
