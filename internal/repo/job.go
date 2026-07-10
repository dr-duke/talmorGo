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

const jobSelect = `SELECT id, url, status, title, error, source, chat_id, created_at, updated_at, retry_count, next_retry_at, first_failed_at, COALESCE(tg_message_id,0) FROM jobs`

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
		`INSERT INTO jobs (id, url, status, title, error, source, chat_id, created_at, updated_at, retry_count)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		job.ID, job.URL, job.Status, job.Title, job.Error,
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
// Все запросы должны возвращать ровно эти колонки в этом порядке.
const mediaRowSQL = `
	SELECT
		j.id, j.url, j.status, j.title,
		j.error, j.source, j.chat_id,
		j.created_at, j.updated_at, j.retry_count, j.next_retry_at, j.first_failed_at,
		j.hidden,
		i.id, i.kind, i.name, i.size, i.path, i.duration,
		i.title, i.artist, i.album, i.year, i.genre,
		i.created_at, i.deleted_at, i.lost_at,
		(SELECT GROUP_CONCAT(t2.name,'|')
		 FROM job_tags jt2 JOIN tags t2 ON t2.id=jt2.tag_id WHERE jt2.job_id=j.id) AS tags,
		COALESCE(i.created_at, j.created_at) AS sort_ts
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
//   - одну строку на каждый item (один job → N items → N строк)
//   - плюс одну строку на job без items (pending/running/retrying/failed/cancelled)
func (r *sqliteJobRepo) ListMedia(ctx context.Context) ([]*model.MediaItem, error) {
	q := mediaRowSQL + `
		FROM items i
		JOIN jobs j ON j.id = i.job_id
		WHERE j.hidden = 0

		UNION ALL

		` + mediaRowSQL + `
		FROM jobs j
		LEFT JOIN items i ON i.id = NULL
		WHERE j.hidden = 0
		  AND j.status IN ('checking','pending','running','retrying','failed','cancelled')
		  AND NOT EXISTS (SELECT 1 FROM items WHERE job_id = j.id)

		ORDER BY sort_ts DESC
	`
	return r.runMediaQuery(ctx, q)
}

// SearchMedia ищет по имени, URL, заголовку и тегам (для Telegram-бота).
func (r *sqliteJobRepo) SearchMedia(ctx context.Context, query string) ([]*model.MediaItem, error) {
	like := "%" + query + "%"
	q := mediaRowSQL + `
		FROM items i
		JOIN jobs j ON j.id = i.job_id
		WHERE j.hidden = 0
		  AND (i.name LIKE ? OR j.url LIKE ? OR j.title LIKE ?
		       OR j.id IN (SELECT jt2.job_id FROM job_tags jt2
		                   JOIN tags t2 ON t2.id=jt2.tag_id WHERE t2.name LIKE ?))

		UNION ALL

		` + mediaRowSQL + `
		FROM jobs j
		LEFT JOIN items i ON i.id = NULL
		WHERE j.hidden = 0
		  AND j.status IN ('checking','pending','running','retrying','failed','cancelled')
		  AND NOT EXISTS (SELECT 1 FROM items WHERE job_id = j.id)
		  AND (j.url LIKE ? OR j.title LIKE ?
		       OR j.id IN (SELECT jt2.job_id FROM job_tags jt2
		                   JOIN tags t2 ON t2.id=jt2.tag_id WHERE t2.name LIKE ?))

		ORDER BY sort_ts DESC
		LIMIT 10
	`
	return r.runMediaQuery(ctx, q, like, like, like, like, like, like, like)
}

// FilterMedia — серверная фильтрация: текст + kind + AND-теги.
func (r *sqliteJobRepo) FilterMedia(ctx context.Context, f model.MediaFilter) ([]*model.MediaItem, error) {
	if f.Query == "" && f.Kind == "" && len(f.Tags) == 0 {
		return r.ListMedia(ctx)
	}

	var fileConds []string
	var fileArgs []any
	var jobConds []string
	var jobArgs []any

	if f.Query != "" {
		like := "%" + f.Query + "%"
		fileConds = append(fileConds, "(i.name LIKE ? OR j.url LIKE ? OR j.title LIKE ?)")
		fileArgs = append(fileArgs, like, like, like)
		jobConds = append(jobConds, "(j.url LIKE ? OR j.title LIKE ?)")
		jobArgs = append(jobArgs, like, like)
	}

	if f.Kind != "" {
		fileConds = append(fileConds, "i.kind=?")
		fileArgs = append(fileArgs, f.Kind)
		// для pending-строк kind не применяется (нет item)
	}

	for _, tag := range f.Tags {
		sub := `j.id IN (SELECT jt.job_id FROM job_tags jt JOIN tags t ON t.id=jt.tag_id WHERE t.name=?)`
		fileConds = append(fileConds, sub)
		fileArgs = append(fileArgs, tag)
		jobConds = append(jobConds, sub)
		jobArgs = append(jobArgs, tag)
	}

	fileWhere := " WHERE j.hidden=0"
	if len(fileConds) > 0 {
		fileWhere += " AND " + strings.Join(fileConds, " AND ")
	}
	jobAnd := ""
	if len(jobConds) > 0 {
		jobAnd = " AND " + strings.Join(jobConds, " AND ")
	}

	// Если фильтр по kind — показываем только items нужного типа, без pending-строк
	// (у pending нет items, поэтому kind=audio/video логично исключает их).
	pendingPart := ""
	if f.Kind == "" {
		pendingPart = `
			UNION ALL

			` + mediaRowSQL + `
			FROM jobs j
			LEFT JOIN items i ON i.id = NULL
			WHERE j.hidden = 0
			  AND j.status IN ('checking','pending','running','retrying','failed','cancelled')
			  AND NOT EXISTS (SELECT 1 FROM items WHERE job_id = j.id)
			` + jobAnd
	}

	q := mediaRowSQL + `
		FROM items i
		JOIN jobs j ON j.id = i.job_id
		` + fileWhere + `
		` + pendingPart + `

		ORDER BY sort_ts DESC
	`
	allArgs := append(fileArgs, jobArgs...)
	return r.runMediaQuery(ctx, q, allArgs...)
}

// LastMedia возвращает последние n доступных элементов.
func (r *sqliteJobRepo) LastMedia(ctx context.Context, n int) ([]*model.MediaItem, error) {
	q := mediaRowSQL + `
		FROM items i
		JOIN jobs j ON j.id = i.job_id
		WHERE j.status IN ('done','imported') AND i.deleted_at IS NULL AND i.lost_at IS NULL
		ORDER BY sort_ts DESC
		LIMIT ?
	`
	return r.runMediaQuery(ctx, q, n)
}

func scanMediaItem(s scanner) (*model.MediaItem, error) {
	var j model.Job
	var createdAt, updatedAt string
	var nextRetryAt, firstFailedAt sql.NullString
	var hidden int

	var itemID, itemKind, itemName, itemPath sql.NullString
	var itemSize sql.NullInt64
	var itemDuration sql.NullInt64
	var itemTitle, itemArtist, itemAlbum, itemYear, itemGenre sql.NullString
	var itemCreatedAt, itemDeletedAt, itemLostAt sql.NullString
	var tagNames sql.NullString
	var sortTS sql.NullString

	err := s.Scan(
		&j.ID, &j.URL, &j.Status, &j.Title,
		&j.Error, &j.Source, &j.ChatID,
		&createdAt, &updatedAt, &j.RetryCount, &nextRetryAt, &firstFailedAt,
		&hidden,
		&itemID, &itemKind, &itemName, &itemSize, &itemPath, &itemDuration,
		&itemTitle, &itemArtist, &itemAlbum, &itemYear, &itemGenre,
		&itemCreatedAt, &itemDeletedAt, &itemLostAt,
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
	j.Hidden = hidden != 0

	media := &model.MediaItem{Job: &j}

	if itemID.Valid && itemID.String != "" {
		item := &model.Item{
			ID:       itemID.String,
			JobID:    j.ID,
			Kind:     itemKind.String,
			Name:     itemName.String,
			Size:     itemSize.Int64,
			Path:     itemPath.String,
			Duration: int(itemDuration.Int64),
			Meta: model.AudioMeta{
				Title:  itemTitle.String,
				Artist: itemArtist.String,
				Album:  itemAlbum.String,
				Year:   itemYear.String,
				Genre:  itemGenre.String,
			},
		}
		item.CreatedAt, _ = time.Parse(time.RFC3339Nano, itemCreatedAt.String)
		if itemDeletedAt.Valid && itemDeletedAt.String != "" {
			t, _ := time.Parse(time.RFC3339Nano, itemDeletedAt.String)
			item.DeletedAt = &t
		}
		if itemLostAt.Valid && itemLostAt.String != "" {
			t, _ := time.Parse(time.RFC3339Nano, itemLostAt.String)
			item.LostAt = &t
		}
		media.Item = item
	}

	if tagNames.Valid && tagNames.String != "" {
		media.Tags = strings.Split(tagNames.String, "|")
	}

	return media, nil
}

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
		 RETURNING id, url, status, title, error, source, chat_id, created_at, updated_at, retry_count, next_retry_at, first_failed_at, COALESCE(tg_message_id,0)`,
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
		`UPDATE jobs SET status=?, title=?, error=?, retry_count=?, next_retry_at=?, first_failed_at=?, updated_at=? WHERE id=?`,
		job.Status, job.Title, job.Error,
		job.RetryCount, nextRetry, firstFailed,
		job.UpdatedAt.Format(time.RFC3339Nano), job.ID,
	)
	return err
}

func (r *sqliteJobRepo) Cancel(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status='cancelled', next_retry_at=NULL, updated_at=? WHERE id=? AND status IN ('checking','pending','retrying')`,
		time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("job %s not found or not cancellable", id)
	}
	return nil
}

func (r *sqliteJobRepo) ConfirmSingle(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status='pending', updated_at=? WHERE id=? AND status='checking'`,
		time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	return err
}

func (r *sqliteJobRepo) DeleteChecking(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM jobs WHERE id=? AND status='checking'`, id)
	return err
}

func (r *sqliteJobRepo) ResetFailed(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status='pending', error='', retry_count=0, next_retry_at=NULL, first_failed_at=NULL, updated_at=?
		 WHERE id=? AND status IN ('failed','retrying')`,
		time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("job %s not found or not failed/retrying", id)
	}
	return nil
}

func (r *sqliteJobRepo) Redownload(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status='checking', title='', error='',
		  retry_count=0, next_retry_at=NULL, first_failed_at=NULL, hidden=0, updated_at=?
		 WHERE id=?`,
		time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	return err
}

func (r *sqliteJobRepo) Hide(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET hidden=1, updated_at=? WHERE id=?`,
		time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	return err
}

func (r *sqliteJobRepo) Unhide(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET hidden=0, updated_at=? WHERE id=?`,
		time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	return err
}

func (r *sqliteJobRepo) ResetStale(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status='pending', updated_at=? WHERE status IN ('running','checking')`,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

func (r *sqliteJobRepo) Purge(ctx context.Context, id string) error {
	if _, err := r.db.ExecContext(ctx, `DELETE FROM items WHERE job_id=?`, id); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, `DELETE FROM jobs WHERE id=? AND hidden=1`, id)
	return err
}

func (r *sqliteJobRepo) CleanupDead(ctx context.Context) (int, error) {
	if _, err := r.db.ExecContext(ctx, `
		DELETE FROM items WHERE job_id IN (
			SELECT id FROM jobs WHERE hidden=1 OR status='failed'
		)`); err != nil {
		return 0, err
	}
	if _, err := r.db.ExecContext(ctx, `
		DELETE FROM job_tags WHERE job_id IN (
			SELECT id FROM jobs WHERE hidden=1 OR status='failed'
		)`); err != nil {
		return 0, err
	}
	res, err := r.db.ExecContext(ctx, `DELETE FROM jobs WHERE hidden=1 OR status='failed'`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (r *sqliteJobRepo) SetTgMessageID(ctx context.Context, jobID string, msgID int64) error {
	_, err := r.db.ExecContext(ctx, `UPDATE jobs SET tg_message_id=? WHERE id=?`, msgID, jobID)
	return err
}

func (r *sqliteJobRepo) SaveLog(ctx context.Context, jobID, log string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE jobs SET last_log=? WHERE id=?`, log, jobID)
	return err
}

func (r *sqliteJobRepo) GetLog(ctx context.Context, jobID string) (string, error) {
	var v sql.NullString
	err := r.db.QueryRowContext(ctx, `SELECT last_log FROM jobs WHERE id=?`, jobID).Scan(&v)
	if err != nil {
		return "", err
	}
	return v.String, nil
}

type scanner interface {
	Scan(dest ...any) error
}

func scanJob(s scanner) (*model.Job, error) {
	var j model.Job
	var createdAt, updatedAt string
	var nextRetryAt, firstFailedAt sql.NullString
	err := s.Scan(
		&j.ID, &j.URL, &j.Status, &j.Title, &j.Error,
		&j.Source, &j.ChatID, &createdAt, &updatedAt,
		&j.RetryCount, &nextRetryAt, &firstFailedAt, &j.TgMessageID,
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
