package repo

import (
	"context"
	"database/sql"
	"strings"

	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/google/uuid"
)

type sqliteTagRepo struct {
	db *sql.DB
}

func NewTagRepo(db *sql.DB) TagRepo {
	return &sqliteTagRepo{db: db}
}

// Upsert возвращает существующий тег или создаёт новый.
func (r *sqliteTagRepo) Upsert(ctx context.Context, name string) (*model.Tag, error) {
	id := uuid.NewString()
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO tags (id, name) VALUES (?, ?) ON CONFLICT(name) DO NOTHING`, id, name)
	if err != nil {
		return nil, err
	}
	row := r.db.QueryRowContext(ctx, `SELECT id, name, kind FROM tags WHERE name=?`, name)
	var t model.Tag
	if err := row.Scan(&t.ID, &t.Name, &t.Kind); err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *sqliteTagRepo) ListAll(ctx context.Context) ([]*model.Tag, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, name, kind FROM tags ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tags []*model.Tag
	for rows.Next() {
		var t model.Tag
		if err := rows.Scan(&t.ID, &t.Name, &t.Kind); err != nil {
			return nil, err
		}
		tags = append(tags, &t)
	}
	return tags, rows.Err()
}

func (r *sqliteTagRepo) AddToJob(ctx context.Context, jobID, tagID string) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO job_tags (job_id, tag_id) VALUES (?, ?)`, jobID, tagID)
	return err
}

func (r *sqliteTagRepo) RemoveFromJob(ctx context.Context, jobID, tagName string) error {
	if _, err := r.db.ExecContext(ctx,
		`DELETE FROM job_tags WHERE job_id=? AND tag_id=(SELECT id FROM tags WHERE name=?)`,
		jobID, tagName); err != nil {
		return err
	}
	// Удаляем тег если он больше ни к чему не привязан (коллекции не трогаем).
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM tags WHERE name=? AND kind='plain'
		 AND id NOT IN (SELECT DISTINCT tag_id FROM job_tags)`,
		tagName)
	return err
}

func (r *sqliteTagRepo) ListWithCount(ctx context.Context) ([]*model.TagWithCount, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT t.name, COUNT(j.id) AS cnt,
		       CASE WHEN c.id IS NOT NULL THEN 1 ELSE 0 END AS is_coll
		FROM tags t
		LEFT JOIN job_tags jt ON jt.tag_id = t.id
		LEFT JOIN jobs j ON j.id = jt.job_id AND j.hidden = 0
		LEFT JOIN collections c ON c.name = t.name
		GROUP BY t.id
		HAVING cnt > 0 OR t.kind = 'collection'
		ORDER BY is_coll DESC, cnt DESC, t.name ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.TagWithCount
	for rows.Next() {
		var tw model.TagWithCount
		var isCol int
		if err := rows.Scan(&tw.Name, &tw.Count, &isCol); err != nil {
			return nil, err
		}
		tw.IsCollection = isCol == 1
		out = append(out, &tw)
	}
	return out, rows.Err()
}

func (r *sqliteTagRepo) BulkAddToJobs(ctx context.Context, tagID string, jobIDs []string) error {
	if len(jobIDs) == 0 {
		return nil
	}
	placeholders := make([]string, len(jobIDs))
	args := make([]any, 0, len(jobIDs)*2)
	for i, id := range jobIDs {
		placeholders[i] = "(?, ?)"
		args = append(args, id, tagID)
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO job_tags (job_id, tag_id) VALUES `+strings.Join(placeholders, ","),
		args...)
	return err
}

func (r *sqliteTagRepo) PruneOrphans(ctx context.Context) (nJobTags, nTags, nCollections int, err error) {
	exec := func(q string) (int, error) {
		res, e := r.db.ExecContext(ctx, q)
		if e != nil {
			return 0, e
		}
		n, _ := res.RowsAffected()
		return int(n), nil
	}

	// 1. job_tags → несуществующие jobs
	if nJobTags, err = exec(`DELETE FROM job_tags WHERE job_id NOT IN (SELECT id FROM jobs)`); err != nil {
		return
	}
	// 2. plain теги без привязанных заданий
	if nTags, err = exec(`DELETE FROM tags WHERE kind='plain' AND id NOT IN (SELECT DISTINCT tag_id FROM job_tags)`); err != nil {
		return
	}
	// 3. коллекции без активных заданий
	if nCollections, err = exec(`
		DELETE FROM collections WHERE id NOT IN (
			SELECT DISTINCT c.id FROM collections c
			JOIN tags t ON t.name = c.name
			JOIN job_tags jt ON jt.tag_id = t.id
			JOIN jobs j ON j.id = jt.job_id
		)`); err != nil {
		return
	}
	// 4. collection-теги без соответствующей записи в collections
	n, e := exec(`DELETE FROM tags WHERE kind='collection' AND name NOT IN (SELECT name FROM collections)`)
	nTags += n
	err = e
	return
}
