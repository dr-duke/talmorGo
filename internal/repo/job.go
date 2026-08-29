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

// jobColumnList — набор колонок, который умеет разбирать scanJob.
// Используется и в SELECT, и в RETURNING у ClaimNext: списки обязаны совпадать.
const jobColumnList = `id, url, status, title, error, source, chat_id,
	created_at, updated_at, retry_count, next_retry_at, first_failed_at,
	COALESCE(tg_message_id,0), hidden`

const jobSelect = `SELECT ` + jobColumnList + ` FROM jobs`

type sqliteJobRepo struct {
	db *db.DB
}

func NewJobRepo(database *db.DB) JobRepo {
	return &sqliteJobRepo{db: database}
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

// ListIDsByStatus возвращает идентификаторы заданий в указанных статусах.
// Нужен для массовой отмены: воркеру передаются id активных загрузок.
func (r *sqliteJobRepo) ListIDsByStatus(ctx context.Context, statuses ...model.JobStatus) ([]string, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(statuses))
	args := make([]any, len(statuses))
	for i, s := range statuses {
		placeholders[i] = "?"
		args[i] = s
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id FROM jobs WHERE status IN (`+strings.Join(placeholders, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

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

// FilterMedia — серверная фильтрация медиатеки: текст + тип + AND-теги,
// с постраничной выдачей. Пустой фильтр возвращает всю медиатеку.
func (r *sqliteJobRepo) FilterMedia(ctx context.Context, f model.MediaFilter) ([]*model.MediaItem, error) {
	q, args := buildMediaQuery(f)
	return r.runMediaQuery(ctx, q, args...)
}

// CountMedia возвращает полное число строк для фильтра, без Limit/Offset.
func (r *sqliteJobRepo) CountMedia(ctx context.Context, f model.MediaFilter) (int, error) {
	q, args := buildMediaCountQuery(f)
	var n int
	err := r.db.QueryRowContext(ctx, q, args...).Scan(&n)
	return n, err
}

// SearchMedia — поиск для Telegram: тот же фильтр, что и в вебе, с лимитом выдачи.
func (r *sqliteJobRepo) SearchMedia(ctx context.Context, query string) ([]*model.MediaItem, error) {
	return r.FilterMedia(ctx, model.MediaFilter{Query: query, Limit: 10})
}

// LastMedia возвращает последние n доступных элементов.
func (r *sqliteJobRepo) LastMedia(ctx context.Context, n int) ([]*model.MediaItem, error) {
	q := `SELECT ` + jobColumns + `, ` + itemColumns + `, ` + tagsColumn + ` AS tags,
		COALESCE(i.created_at, j.created_at) AS sort_ts
		FROM items i
		JOIN jobs j ON j.id = i.job_id
		WHERE j.status IN ('done','imported') AND i.deleted_at IS NULL AND i.lost_at IS NULL
		ORDER BY sort_ts DESC
		LIMIT ?`
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
	row := r.db.WriteRowContext(ctx,
		`UPDATE jobs SET status='running', updated_at=?
		 WHERE id = (
		     SELECT id FROM jobs
		     WHERE status='pending'
		        OR (status='retrying' AND next_retry_at <= ?)
		     ORDER BY created_at ASC LIMIT 1
		 )
		 RETURNING `+jobColumnList,
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

func (r *sqliteJobRepo) CancelAll(ctx context.Context) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status='cancelled', next_retry_at=NULL, updated_at=?
		 WHERE status IN ('checking','pending','running','retrying')`,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
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

// ListChecking возвращает задания, зависшие в статусе checking (проверка на плейлист
// не завершилась из-за перезапуска). Их разворачивание нужно запустить заново.
func (r *sqliteJobRepo) ListChecking(ctx context.Context) ([]*model.Job, error) {
	return r.List(ctx, JobFilter{Statuses: []model.JobStatus{model.JobChecking}})
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

// ResetStale возвращает в очередь задания, оборванные рестартом.
// Статус checking не трогаем: такое задание не прошло проверку на плейлист,
// и перевод его в pending скачал бы плейлист одним заданием. Их перезапускает
// очередь через ListChecking.
func (r *sqliteJobRepo) ResetStale(ctx context.Context) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE jobs SET status='pending', updated_at=? WHERE status='running'`,
		time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

// Purge безвозвратно удаляет скрытое задание вместе с его элементами.
// Незакрытые (не скрытые) задания не трогаются: раньше items удалялись до
// проверки hidden, и у обычного задания пропадали записи о файлах.
func (r *sqliteJobRepo) Purge(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx, `DELETE FROM jobs WHERE id=? AND hidden=1`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("job %s not found or not hidden", id)
	}
	// items и job_tags уходят каскадом по внешнему ключу.
	return nil
}

func (r *sqliteJobRepo) CleanupDead(ctx context.Context) (int, error) {
	// items и job_tags удаляются каскадом.
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
	var hidden int
	err := s.Scan(
		&j.ID, &j.URL, &j.Status, &j.Title, &j.Error,
		&j.Source, &j.ChatID, &createdAt, &updatedAt,
		&j.RetryCount, &nextRetryAt, &firstFailedAt, &j.TgMessageID, &hidden,
	)
	if err != nil {
		return nil, err
	}
	j.Hidden = hidden != 0
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
