package repo

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/google/uuid"
)

type sqliteFileRepo struct {
	db *sql.DB
}

func NewFileRepo(db *sql.DB) FileRepo {
	return &sqliteFileRepo{db: db}
}

func (r *sqliteFileRepo) Create(ctx context.Context, f *model.File) error {
	if f.ID == "" {
		f.ID = uuid.NewString()
	}
	f.CreatedAt = time.Now().UTC()

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO files (id, job_id, path, name, size, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			job_id     = excluded.job_id,
			name       = excluded.name,
			size       = excluded.size,
			deleted_at = NULL,
			lost_at    = NULL
	`, f.ID, nullStr(f.JobID), f.Path, f.Name, f.Size, f.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return err
	}

	row := r.db.QueryRowContext(ctx,
		`SELECT id, created_at FROM files WHERE path=?`, f.Path)
	var id, createdAt string
	if err := row.Scan(&id, &createdAt); err != nil {
		return err
	}
	f.ID = id
	f.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	return nil
}

func (r *sqliteFileRepo) GetByID(ctx context.Context, id string) (*model.File, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, COALESCE(job_id,''), path, name, size, created_at, deleted_at, lost_at FROM files WHERE id=?`, id)
	return scanFile(row)
}

func (r *sqliteFileRepo) List(ctx context.Context) ([]*model.File, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, COALESCE(job_id,''), path, name, size, created_at, deleted_at, lost_at
		 FROM files WHERE deleted_at IS NULL ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFiles(rows)
}

// ListAll возвращает все файлы (включая удалённые/потерянные) для проверки файловой системы.
func (r *sqliteFileRepo) ListAll(ctx context.Context) ([]*model.File, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, COALESCE(job_id,''), path, name, size, created_at, deleted_at, lost_at FROM files ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFiles(rows)
}

// ListByJobID возвращает все файлы (включая удалённые) привязанные к заданию.
func (r *sqliteFileRepo) ListByJobID(ctx context.Context, jobID string) ([]*model.File, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, COALESCE(job_id,''), path, name, size, created_at, deleted_at, lost_at
		 FROM files WHERE job_id=? ORDER BY created_at ASC`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFiles(rows)
}

// DeleteAllByJobID физически удаляет все файлы задания из БД (при redownload).
// Presigned-токены удаляются каскадно.
func (r *sqliteFileRepo) DeleteAllByJobID(ctx context.Context, jobID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM files WHERE job_id=?`, jobID)
	return err
}

// AllPaths возвращает множество всех известных путей (включая удалённые)
// для быстрой проверки при сканировании директории.
func (r *sqliteFileRepo) AllPaths(ctx context.Context) (map[string]struct{}, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT path FROM files`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	paths := make(map[string]struct{})
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		paths[p] = struct{}{}
	}
	return paths, rows.Err()
}

func (r *sqliteFileRepo) ListDeleted(ctx context.Context) ([]*model.DeletedFile, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT f.id, f.name, COALESCE(j.url,''), f.deleted_at
		FROM files f
		LEFT JOIN jobs j ON j.file_id = f.id
		WHERE f.deleted_at IS NOT NULL
		GROUP BY f.id
		ORDER BY f.deleted_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []*model.DeletedFile
	for rows.Next() {
		var d model.DeletedFile
		var deletedAt string
		if err := rows.Scan(&d.ID, &d.Name, &d.OriginalURL, &deletedAt); err != nil {
			return nil, err
		}
		d.DeletedAt, _ = time.Parse(time.RFC3339Nano, deletedAt)
		result = append(result, &d)
	}
	return result, rows.Err()
}

func (r *sqliteFileRepo) Rename(ctx context.Context, id, newName, newPath string) error {
	res, err := r.db.ExecContext(ctx, `UPDATE files SET name=?, path=? WHERE id=?`, newName, newPath, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("file %s not found", id)
	}
	return nil
}

func (r *sqliteFileRepo) Delete(ctx context.Context, id string) error {
	res, err := r.db.ExecContext(ctx,
		`UPDATE files SET deleted_at=? WHERE id=? AND deleted_at IS NULL`,
		time.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("file %s not found or already deleted", id)
	}
	return nil
}

func (r *sqliteFileRepo) MarkLost(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE files SET lost_at=? WHERE id=? AND lost_at IS NULL AND deleted_at IS NULL`,
		time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

func (r *sqliteFileRepo) MarkFound(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE files SET lost_at=NULL WHERE id=?`, id)
	return err
}

func scanFiles(rows *sql.Rows) ([]*model.File, error) {
	var files []*model.File
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

func scanFile(s scanner) (*model.File, error) {
	var f model.File
	var createdAt string
	var deletedAt, lostAt sql.NullString
	err := s.Scan(&f.ID, &f.JobID, &f.Path, &f.Name, &f.Size, &createdAt, &deletedAt, &lostAt)
	if err != nil {
		return nil, err
	}
	f.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	if deletedAt.Valid && deletedAt.String != "" {
		t, _ := time.Parse(time.RFC3339Nano, deletedAt.String)
		f.DeletedAt = &t
	}
	if lostAt.Valid && lostAt.String != "" {
		t, _ := time.Parse(time.RFC3339Nano, lostAt.String)
		f.LostAt = &t
	}
	return &f, nil
}
