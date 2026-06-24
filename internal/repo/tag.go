package repo

import (
	"context"
	"database/sql"

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
