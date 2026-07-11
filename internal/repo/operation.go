package repo

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/google/uuid"
)

type sqliteOperationRepo struct {
	db *sql.DB
}

func NewOperationRepo(db *sql.DB) OperationRepo {
	return &sqliteOperationRepo{db: db}
}

func (r *sqliteOperationRepo) Create(ctx context.Context, op *model.Operation) error {
	if op.ID == "" {
		op.ID = uuid.NewString()
	}
	if op.CreatedAt.IsZero() {
		op.CreatedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO operations (id, kind, status, title, payload, created_at)
		 VALUES (?, ?, 'pending', ?, ?, ?)`,
		op.ID, op.Kind, op.Title, op.Payload,
		op.CreatedAt.Format(time.RFC3339Nano),
	)
	return err
}

func (r *sqliteOperationRepo) ClaimNext(ctx context.Context) (*model.Operation, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() //nolint:errcheck

	var op model.Operation
	var createdAt string
	err = tx.QueryRowContext(ctx,
		`SELECT id, kind, title, payload, created_at FROM operations WHERE status='pending' ORDER BY created_at LIMIT 1`,
	).Scan(&op.ID, &op.Kind, &op.Title, &op.Payload, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := tx.ExecContext(ctx,
		`UPDATE operations SET status='running', started_at=? WHERE id=?`, now, op.ID,
	); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	op.Status = model.OpRunning
	op.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	return &op, nil
}

func (r *sqliteOperationRepo) SetDone(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE operations SET status='done', finished_at=? WHERE id=?`,
		time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	return err
}

func (r *sqliteOperationRepo) SetFailed(ctx context.Context, id, errMsg string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE operations SET status='failed', finished_at=?, error=? WHERE id=?`,
		time.Now().UTC().Format(time.RFC3339Nano), errMsg, id,
	)
	return err
}

func (r *sqliteOperationRepo) List(ctx context.Context, kinds []string) ([]*model.Operation, error) {
	var rows *sql.Rows
	var err error

	if len(kinds) == 0 {
		rows, err = r.db.QueryContext(ctx,
			`SELECT id, kind, status, title, payload, created_at, COALESCE(started_at,''), COALESCE(finished_at,''), COALESCE(error,'')
			 FROM operations ORDER BY created_at DESC`)
	} else {
		placeholders := make([]string, len(kinds))
		args := make([]any, len(kinds))
		for i, k := range kinds {
			placeholders[i] = "?"
			args[i] = k
		}
		q := `SELECT id, kind, status, title, payload, created_at, COALESCE(started_at,''), COALESCE(finished_at,''), COALESCE(error,'')
			  FROM operations WHERE kind IN (` + strings.Join(placeholders, ",") + `) ORDER BY created_at DESC`
		rows, err = r.db.QueryContext(ctx, q, args...)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*model.Operation
	for rows.Next() {
		op, err := scanOperation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

func (r *sqliteOperationRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM operations WHERE id=?`, id)
	return err
}

func scanOperation(s interface {
	Scan(...any) error
}) (*model.Operation, error) {
	var op model.Operation
	var createdAt, startedAt, finishedAt string
	err := s.Scan(
		&op.ID, &op.Kind, &op.Status, &op.Title, &op.Payload,
		&createdAt, &startedAt, &finishedAt, &op.Error,
	)
	if err != nil {
		return nil, err
	}
	op.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	if startedAt != "" {
		t, _ := time.Parse(time.RFC3339Nano, startedAt)
		op.StartedAt = &t
	}
	if finishedAt != "" {
		t, _ := time.Parse(time.RFC3339Nano, finishedAt)
		op.FinishedAt = &t
	}
	return &op, nil
}
