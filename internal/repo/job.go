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

const jobSelect = `SELECT id, url, status, title, COALESCE(file_id,''), error, source, chat_id, created_at, updated_at, retry_count, next_retry_at, first_failed_at FROM jobs`

type sqliteJobRepo struct {
	db *sql.DB
}

func NewJobRepo(db *sql.DB) JobRepo {
	return &sqliteJobRepo{db: db}
}

func (r *sqliteJobRepo) Create(ctx context.Context, job *model.Job) error {
	if job.ID == "" {
		job.ID = uuid.NewString()
	}
	now := time.Now().UTC()
	job.CreatedAt = now
	job.UpdatedAt = now
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO jobs (id, url, status, title, file_id, error, source, chat_id, created_at, updated_at, retry_count)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		job.ID, job.URL, job.Status, job.Title, nullStr(job.FileID), job.Error,
		job.Source, job.ChatID,
		job.CreatedAt.Format(time.RFC3339Nano),
		job.UpdatedAt.Format(time.RFC3339Nano),
	)
	return err
}

func (r *sqliteJobRepo) GetByID(ctx context.Context, id string) (*model.Job, error) {
	row := r.db.QueryRowContext(ctx, jobSelect+` WHERE id = ?`, id)
	return scanJob(row)
}

func (r *sqliteJobRepo) List(ctx context.Context, f JobFilter) ([]*model.Job, error) {
	query := jobSelect
	var args []any
	if len(f.Statuses) > 0 {
		placeholders := make([]string, len(f.Statuses))
		for i, s := range f.Statuses {
			placeholders[i] = "?"
			args = append(args, s)
		}
		query += " WHERE status IN (" + strings.Join(placeholders, ",") + ")"
	}
	query += " ORDER BY created_at DESC"

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*model.Job
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, j)
	}
	return jobs, rows.Err()
}

// ClaimNext атомарно берёт ближайшую pending-задачу или retrying-задачу с наступившим временем повтора.
func (r *sqliteJobRepo) ClaimNext(ctx context.Context) (*model.Job, error) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	row := r.db.QueryRowContext(ctx,
		`UPDATE jobs SET status='running', updated_at=?
		 WHERE id = (
		     SELECT id FROM jobs
		     WHERE status='pending'
		        OR (status='retrying' AND next_retry_at <= ?)
		     ORDER BY created_at ASC LIMIT 1
		 )
		 RETURNING id, url, status, title, COALESCE(file_id,''), error, source, chat_id, created_at, updated_at, retry_count, next_retry_at, first_failed_at`,
		now, now,
	)
	j, err := scanJob(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return j, err
}

func (r *sqliteJobRepo) Update(ctx context.Context, job *model.Job) error {
	job.UpdatedAt = time.Now().UTC()
	var nextRetry, firstFailed any
	if job.NextRetryAt != nil {
		nextRetry = job.NextRetryAt.UTC().Format(time.RFC3339Nano)
	}
	if job.FirstFailedAt != nil {
		firstFailed = job.FirstFailedAt.UTC().Format(time.RFC3339Nano)
	}
	_, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status=?, title=?, file_id=?, error=?, retry_count=?, next_retry_at=?, first_failed_at=?, updated_at=? WHERE id=?`,
		job.Status, job.Title, nullStr(job.FileID), job.Error,
		job.RetryCount, nextRetry, firstFailed,
		job.UpdatedAt.Format(time.RFC3339Nano), job.ID,
	)
	return err
}

func (r *sqliteJobRepo) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM jobs WHERE id=? AND status='pending'`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("job %s not found or not pending", id)
	}
	return nil
}

// ResetFailed переводит failed-задачу обратно в pending с обнулением счётчиков retry.
func (r *sqliteJobRepo) ResetFailed(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status='pending', error='', retry_count=0, next_retry_at=NULL, first_failed_at=NULL, updated_at=?
		 WHERE id=? AND status='failed'`,
		time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("job %s not found or not failed", id)
	}
	return nil
}

func (r *sqliteJobRepo) ResetStale(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status='pending', updated_at=? WHERE status='running'`,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

// scanner объединяет *sql.Row и *sql.Rows для переиспользования scanJob.
type scanner interface {
	Scan(dest ...any) error
}

func scanJob(s scanner) (*model.Job, error) {
	var j model.Job
	var createdAt, updatedAt string
	var nextRetryAt, firstFailedAt sql.NullString
	err := s.Scan(
		&j.ID, &j.URL, &j.Status, &j.Title, &j.FileID, &j.Error,
		&j.Source, &j.ChatID, &createdAt, &updatedAt,
		&j.RetryCount, &nextRetryAt, &firstFailedAt,
	)
	if err != nil {
		return nil, err
	}
	j.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	j.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	if nextRetryAt.Valid && nextRetryAt.String != "" {
		t, _ := time.Parse(time.RFC3339Nano, nextRetryAt.String)
		j.NextRetryAt = &t
	}
	if firstFailedAt.Valid && firstFailedAt.String != "" {
		t, _ := time.Parse(time.RFC3339Nano, firstFailedAt.String)
		j.FirstFailedAt = &t
	}
	return &j, nil
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}
