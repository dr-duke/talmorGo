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
		       COUNT(jt.job_id) AS job_count
		FROM collections c
		LEFT JOIN tags t ON t.name = c.name
		LEFT JOIN job_tags jt ON jt.tag_id = t.id
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
	var name string
	if err := r.db.QueryRowContext(ctx, `SELECT name FROM collections WHERE id=?`, id).Scan(&name); err != nil {
		return fmt.Errorf("collection %s not found", id)
	}
	// Remove tag assignments for this collection name.
	r.db.ExecContext(ctx, `DELETE FROM job_tags WHERE tag_id = (SELECT id FROM tags WHERE name=?)`, name) //nolint:errcheck
	// Remove the tag itself.
	r.db.ExecContext(ctx, `DELETE FROM tags WHERE name=?`, name) //nolint:errcheck
	_, err := r.db.ExecContext(ctx, `DELETE FROM collections WHERE id=?`, id)
	return err
}

func (r *sqliteCollectionRepo) Rename(ctx context.Context, id, newName string) error {
	var oldName string
	if err := r.db.QueryRowContext(ctx, `SELECT name FROM collections WHERE id=?`, id).Scan(&oldName); err != nil {
		return fmt.Errorf("collection %s not found", id)
	}
	if _, err := r.db.ExecContext(ctx, `UPDATE collections SET name=? WHERE id=?`, newName, id); err != nil {
		return err
	}
	// Rename the backing tag so existing assignments follow the new name.
	r.db.ExecContext(ctx, `UPDATE tags SET name=? WHERE name=?`, newName, oldName) //nolint:errcheck
	return nil
}

// AddJobs связывает набор заданий с коллекцией через тег (имя коллекции = имя тега).
func (r *sqliteCollectionRepo) AddJobs(ctx context.Context, collectionID string, jobIDs []string) error {
	if len(jobIDs) == 0 {
		return nil
	}
	var name string
	if err := r.db.QueryRowContext(ctx, `SELECT name FROM collections WHERE id=?`, collectionID).Scan(&name); err != nil {
		return fmt.Errorf("collection %s not found", collectionID)
	}

	// Upsert tag.
	newID := uuid.NewString()
	r.db.ExecContext(ctx, `INSERT OR IGNORE INTO tags (id, name) VALUES (?, ?)`, newID, name) //nolint:errcheck

	var tagID string
	if err := r.db.QueryRowContext(ctx, `SELECT id FROM tags WHERE name=?`, name).Scan(&tagID); err != nil {
		return fmt.Errorf("tag lookup failed: %w", err)
	}

	// Bulk insert into job_tags.
	placeholders := make([]string, len(jobIDs))
	args := make([]any, 0, len(jobIDs)*2)
	for i, jid := range jobIDs {
		placeholders[i] = "(?, ?)"
		args = append(args, jid, tagID)
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO job_tags (job_id, tag_id) VALUES `+strings.Join(placeholders, ","),
		args...)
	return err
}
