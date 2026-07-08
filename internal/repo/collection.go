package repo

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/google/uuid"
)

type sqliteCollectionRepo struct {
	db *sql.DB
}

func NewCollectionRepo(db *sql.DB) CollectionRepo {
	return &sqliteCollectionRepo{db: db}
}

func (r *sqliteCollectionRepo) List(ctx context.Context) ([]*model.Collection, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT c.id, c.name, c.created_at,
		       COUNT(cj.job_id) AS job_count
		FROM collections c
		LEFT JOIN collection_jobs cj ON cj.collection_id = c.id
		GROUP BY c.id
		ORDER BY c.created_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.Collection
	for rows.Next() {
		var c model.Collection
		var createdAt string
		if err := rows.Scan(&c.ID, &c.Name, &createdAt, &c.JobCount); err != nil {
			return nil, err
		}
		c.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
		out = append(out, &c)
	}
	return out, rows.Err()
}

func (r *sqliteCollectionRepo) Create(ctx context.Context, name string) (*model.Collection, error) {
	c := &model.Collection{
		ID:        uuid.NewString(),
		Name:      name,
		CreatedAt: time.Now().UTC(),
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO collections (id, name, created_at) VALUES (?, ?, ?)`,
		c.ID, c.Name, c.CreatedAt.Format(time.RFC3339Nano))
	return c, err
}

func (r *sqliteCollectionRepo) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM collections WHERE id=?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("collection %s not found", id)
	}
	return nil
}

func (r *sqliteCollectionRepo) AddJobs(ctx context.Context, collectionID string, jobIDs []string) error {
	if len(jobIDs) == 0 {
		return nil
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	placeholders := make([]string, len(jobIDs))
	args := make([]any, 0, len(jobIDs)*3)
	for i, id := range jobIDs {
		placeholders[i] = "(?, ?, ?)"
		args = append(args, collectionID, id, now)
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO collection_jobs (collection_id, job_id, added_at) VALUES `+
			strings.Join(placeholders, ","),
		args...)
	return err
}

func (r *sqliteCollectionRepo) RemoveJob(ctx context.Context, collectionID, jobID string) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM collection_jobs WHERE collection_id=? AND job_id=?`,
		collectionID, jobID)
	return err
}

func (r *sqliteCollectionRepo) Rename(ctx context.Context, id, name string) error {
	res, err := r.db.ExecContext(ctx, `UPDATE collections SET name=? WHERE id=?`, name, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("collection %s not found", id)
	}
	return nil
}
