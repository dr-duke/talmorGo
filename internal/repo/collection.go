package repo

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/dr-duke/talmorGo/internal/db"
	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/google/uuid"
)

type sqliteCollectionRepo struct {
	db *db.DB
}

func NewCollectionRepo(database *db.DB) CollectionRepo {
	return &sqliteCollectionRepo{db: database}
}

func (r *sqliteCollectionRepo) List(ctx context.Context) ([]*model.Collection, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT c.id, c.name, c.created_at,
		       COUNT(jt.job_id) AS item_count
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
		if err := rows.Scan(&c.ID, &c.Name, &createdAt, &c.ItemCount); err != nil {
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
	if err != nil {
		return nil, err
	}
	return c, nil
}

// Delete удаляет коллекцию вместе с её тегом и привязками — одной транзакцией,
// чтобы частичный сбой не оставил осиротевшие job_tags.
func (r *sqliteCollectionRepo) Delete(ctx context.Context, id string) error {
	return r.inTx(ctx, func(tx *sql.Tx) error {
		var name string
		if err := tx.QueryRowContext(ctx, `SELECT name FROM collections WHERE id=?`, id).Scan(&name); err != nil {
			return fmt.Errorf("collection %s not found", id)
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM job_tags WHERE tag_id = (SELECT id FROM tags WHERE name=?)`, name); err != nil {
			return fmt.Errorf("delete collection assignments: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM tags WHERE name=?`, name); err != nil {
			return fmt.Errorf("delete collection tag: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM collections WHERE id=?`, id); err != nil {
			return fmt.Errorf("delete collection: %w", err)
		}
		return nil
	})
}

// Rename переименовывает коллекцию и её тег, сохраняя привязки заданий.
func (r *sqliteCollectionRepo) Rename(ctx context.Context, id, newName string) error {
	return r.inTx(ctx, func(tx *sql.Tx) error {
		var oldName string
		if err := tx.QueryRowContext(ctx, `SELECT name FROM collections WHERE id=?`, id).Scan(&oldName); err != nil {
			return fmt.Errorf("collection %s not found", id)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE collections SET name=? WHERE id=?`, newName, id); err != nil {
			return fmt.Errorf("rename collection: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE tags SET name=? WHERE name=?`, newName, oldName); err != nil {
			return fmt.Errorf("rename collection tag: %w", err)
		}
		return nil
	})
}

// AddJobs связывает набор заданий с коллекцией через тег (имя коллекции = имя тега).
func (r *sqliteCollectionRepo) AddJobs(ctx context.Context, collectionID string, jobIDs []string) error {
	if len(jobIDs) == 0 {
		return nil
	}
	return r.inTx(ctx, func(tx *sql.Tx) error {
		var name string
		if err := tx.QueryRowContext(ctx, `SELECT name FROM collections WHERE id=?`, collectionID).Scan(&name); err != nil {
			return fmt.Errorf("collection %s not found", collectionID)
		}

		if _, err := tx.ExecContext(ctx,
			`INSERT INTO tags (id, name, kind) VALUES (?, ?, 'collection')
			 ON CONFLICT(name) DO UPDATE SET kind='collection'`,
			uuid.NewString(), name); err != nil {
			return fmt.Errorf("upsert collection tag: %w", err)
		}

		var tagID string
		if err := tx.QueryRowContext(ctx, `SELECT id FROM tags WHERE name=?`, name).Scan(&tagID); err != nil {
			return fmt.Errorf("tag lookup failed: %w", err)
		}

		placeholders := make([]string, len(jobIDs))
		args := make([]any, 0, len(jobIDs)*2)
		for i, jid := range jobIDs {
			placeholders[i] = "(?, ?)"
			args = append(args, jid, tagID)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO job_tags (job_id, tag_id) VALUES `+strings.Join(placeholders, ","),
			args...); err != nil {
			return fmt.Errorf("assign jobs to collection: %w", err)
		}
		return nil
	})
}

func (r *sqliteCollectionRepo) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if err := fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}
