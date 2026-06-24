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

// mediaRowSQL — базовая проекция для всех media-запросов.
// Все запросы должны возвращать ровно эти 22 колонки в этом порядке.
const mediaRowSQL = `
	SELECT
		j.id, j.url, j.status, j.title, COALESCE(j.file_id,''),
		j.error, j.source, j.chat_id,
		j.created_at, j.updated_at, j.retry_count, j.next_retry_at, j.first_failed_at,
		f.id, f.name, f.size, f.path, f.created_at, f.deleted_at, f.lost_at,
		(SELECT GROUP_CONCAT(t2.name,'|')
		 FROM job_tags jt2 JOIN tags t2 ON t2.id=jt2.tag_id WHERE jt2.job_id=j.id) AS tags,
		COALESCE(f.created_at, j.created_at) AS sort_ts
`

func (r *sqliteJobRepo) runMediaQuery(ctx context.Context, q string, args ...any) ([]*model.MediaItem, error) {
	rows, err := r.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []*model.MediaItem
	for rows.Next() {
		item, err := scanMediaItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// ListMedia возвращает:
//   - одну строку на каждый файл (один job → N файлов → N строк)
//   - плюс одну строку на job без файлов (pending/running/retrying/failed)
func (r *sqliteJobRepo) ListMedia(ctx context.Context) ([]*model.MediaItem, error) {
	q := mediaRowSQL + `
		FROM files f
		JOIN jobs j ON j.id = f.job_id

		UNION ALL

		` + mediaRowSQL + `
		FROM jobs j
		LEFT JOIN files f ON f.id = NULL  -- всегда NULL, нужно для совпадения колонок
		WHERE j.status IN ('pending','running','retrying','failed')
		  AND NOT EXISTS (SELECT 1 FROM files WHERE job_id = j.id)

		ORDER BY sort_ts DESC
	`
	return r.runMediaQuery(ctx, q)
}

// SearchMedia ищет по имени файла, URL, заголовку и тегам.
func (r *sqliteJobRepo) SearchMedia(ctx context.Context, query string) ([]*model.MediaItem, error) {
	like := "%" + query + "%"
	q := mediaRowSQL + `
		FROM files f
		JOIN jobs j ON j.id = f.job_id
		WHERE (f.name LIKE ? OR j.url LIKE ? OR j.title LIKE ?
		       OR j.id IN (SELECT jt2.job_id FROM job_tags jt2
		                   JOIN tags t2 ON t2.id=jt2.tag_id WHERE t2.name LIKE ?))

		UNION ALL

		` + mediaRowSQL + `
		FROM jobs j
		LEFT JOIN files f ON f.id = NULL
		WHERE j.status IN ('pending','running','retrying','failed')
		  AND NOT EXISTS (SELECT 1 FROM files WHERE job_id = j.id)
		  AND (j.url LIKE ? OR j.title LIKE ?
		       OR j.id IN (SELECT jt2.job_id FROM job_tags jt2
		                   JOIN tags t2 ON t2.id=jt2.tag_id WHERE t2.name LIKE ?))

		ORDER BY sort_ts DESC
		LIMIT 10
	`
	return r.runMediaQuery(ctx, q, like, like, like, like, like, like, like)
}

// LastMedia возвращает последние n скачанных доступных файлов.
func (r *sqliteJobRepo) LastMedia(ctx context.Context, n int) ([]*model.MediaItem, error) {
	q := mediaRowSQL + `
		FROM files f
		JOIN jobs j ON j.id = f.job_id
		WHERE j.status = 'done' AND f.deleted_at IS NULL AND f.lost_at IS NULL
		ORDER BY sort_ts DESC
		LIMIT ?
	`
	return r.runMediaQuery(ctx, q, n)
}

func scanMediaItem(s scanner) (*model.MediaItem, error) {
	var j model.Job
	var createdAt, updatedAt string
	var nextRetryAt, firstFailedAt sql.NullString

	var fileID, fileName, filePath, fileCreatedAt sql.NullString
	var fileSize sql.NullInt64
	var fileDeletedAt, fileLostAt sql.NullString
	var tagNames sql.NullString
	var sortTS sql.NullString // вычисляемый столбец, игнорируется

	err := s.Scan(
		&j.ID, &j.URL, &j.Status, &j.Title, &j.FileID, &j.Error,
		&j.Source, &j.ChatID, &createdAt, &updatedAt,
		&j.RetryCount, &nextRetryAt, &firstFailedAt,
		&fileID, &fileName, &fileSize, &filePath, &fileCreatedAt, &fileDeletedAt, &fileLostAt,
		&tagNames, &sortTS,
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

	item := &model.MediaItem{Job: &j}

	if fileID.Valid && fileID.String != "" {
		f := &model.File{
			ID:   fileID.String,
			Name: fileName.String,
			Size: fileSize.Int64,
			Path: filePath.String,
		}
		f.CreatedAt, _ = time.Parse(time.RFC3339Nano, fileCreatedAt.String)
		if fileDeletedAt.Valid && fileDeletedAt.String != "" {
			t, _ := time.Parse(time.RFC3339Nano, fileDeletedAt.String)
			f.DeletedAt = &t
		}
		if fileLostAt.Valid && fileLostAt.String != "" {
			t, _ := time.Parse(time.RFC3339Nano, fileLostAt.String)
			f.LostAt = &t
		}
		item.File = f
	}

	if tagNames.Valid && tagNames.String != "" {
		item.Tags = strings.Split(tagNames.String, "|")
	}

	return item, nil
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

// Redownload сбрасывает задание в pending, очищает file_id и счётчики retry.
func (r *sqliteJobRepo) Redownload(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status='pending', file_id=NULL, title='', error='',
		  retry_count=0, next_retry_at=NULL, first_failed_at=NULL, updated_at=?
		 WHERE id=?`,
		time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	return err
}

func (r *sqliteJobRepo) ResetStale(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status='pending', updated_at=? WHERE status='running'`,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

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
