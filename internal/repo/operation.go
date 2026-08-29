package repo

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/dr-duke/talmorGo/internal/db"
	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/google/uuid"
)

const operationSelect = `SELECT id, kind, status, title, payload, created_at,
	COALESCE(started_at,''), COALESCE(finished_at,''), COALESCE(error,'') FROM operations`

type sqliteOperationRepo struct {
	db *db.DB
}

func NewOperationRepo(database *db.DB) OperationRepo {
	return &sqliteOperationRepo{db: database}
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

func (r *sqliteOperationRepo) GetByID(ctx context.Context, id string) (*model.Operation, error) {
	row := r.db.QueryRowContext(ctx, operationSelect+` WHERE id=?`, id)
	return scanOperation(row)
}

// ClaimNext атомарно забирает старейшую pending-операцию из указанных видов.
// Разделение по видам позволяет держать несколько исполнителей: лёгкие операции
// не ждут завершения тяжёлых (ffmpeg).
func (r *sqliteOperationRepo) ClaimNext(ctx context.Context, kinds []string) (*model.Operation, error) {
	// Порядок аргументов: сначала started_at из SET, затем виды из WHERE.
	args := []any{time.Now().UTC().Format(time.RFC3339Nano)}

	where := `status='pending'`
	if len(kinds) > 0 {
		placeholders := make([]string, len(kinds))
		for i, k := range kinds {
			placeholders[i] = "?"
			args = append(args, k)
		}
		where += ` AND kind IN (` + strings.Join(placeholders, ",") + `)`
	}

	row := r.db.WriteRowContext(ctx,
		`UPDATE operations SET status='running', started_at=?
		 WHERE id = (SELECT id FROM operations WHERE `+where+` ORDER BY created_at LIMIT 1)
		 RETURNING id, kind, status, title, payload, created_at,
		           COALESCE(started_at,''), COALESCE(finished_at,''), COALESCE(error,'')`,
		args...,
	)
	op, err := scanOperation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return op, err
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
	q := operationSelect
	var args []any
	if len(kinds) > 0 {
		placeholders := make([]string, len(kinds))
		for i, k := range kinds {
			placeholders[i] = "?"
			args = append(args, k)
		}
		q += ` WHERE kind IN (` + strings.Join(placeholders, ",") + `)`
	}
	q += ` ORDER BY created_at DESC`

	rows, err := r.db.QueryContext(ctx, q, args...)
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

// ResetStale возвращает в очередь операции, оборванные рестартом приложения.
// Без этого запись навсегда оставалась бы в статусе running и висела в UI.
func (r *sqliteOperationRepo) ResetStale(ctx context.Context) (int, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE operations SET status='pending', started_at=NULL WHERE status='running'`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// DeleteFinishedBefore подчищает завершённые операции: без этого таблица растёт
// бесконечно — видимые записи пользователь закрывает вручную, а закрывает редко.
func (r *sqliteOperationRepo) DeleteFinishedBefore(ctx context.Context, cutoff time.Time) (int, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM operations
		 WHERE status IN ('done','failed')
		   AND COALESCE(finished_at, created_at) < ?`,
		cutoff.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func scanOperation(s scanner) (*model.Operation, error) {
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
