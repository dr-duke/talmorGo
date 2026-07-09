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
	row := r.db.QueryRowContext(ctx, `SELECT id, name FROM tags WHERE name=?`, name)
	var t model.Tag
	if err := row.Scan(&t.ID, &t.Name); err != nil {
		return nil, err
	}
	return &t, nil
}

func (r *sqliteTagRepo) ListAll(ctx context.Context) ([]*model.Tag, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, name FROM tags ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tags []*model.Tag
	for rows.Next() {
		var t model.Tag
		if err := rows.Scan(&t.ID, &t.Name); err != nil {
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
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM job_tags WHERE job_id=? AND tag_id=(SELECT id FROM tags WHERE name=?)`,
		jobID, tagName)
	return err
}

func (r *sqliteTagRepo) ListWithCount(ctx context.Context) ([]*model.TagWithCount, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT t.name, COUNT(jt.job_id) AS cnt,
		       CASE WHEN c.id IS NOT NULL THEN 1 ELSE 0 END AS is_coll
		FROM tags t
		LEFT JOIN job_tags jt ON jt.tag_id = t.id
		LEFT JOIN collections c ON c.name = t.name
		GROUP BY t.id
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
