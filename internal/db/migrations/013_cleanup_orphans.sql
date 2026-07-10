-- Удаляем осиротевшие job_tags (job_id ссылается на несуществующий job)
DELETE FROM job_tags WHERE job_id NOT IN (SELECT id FROM jobs);

-- Удаляем коллекции без единого привязанного job
DELETE FROM collections
WHERE id NOT IN (
    SELECT DISTINCT c.id
    FROM collections c
    JOIN tags t ON t.name = c.name
    JOIN job_tags jt ON jt.tag_id = t.id
    JOIN jobs j ON j.id = jt.job_id
);
