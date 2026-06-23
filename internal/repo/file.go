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

	// Upsert по path: если файл с таким путём уже есть (повторная загрузка),
	// обновляем имя/размер и снимаем soft-delete пометку.
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO files (id, path, name, size, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			name       = excluded.name,
			size       = excluded.size,
			deleted_at = NULL
	`, f.ID, f.Path, f.Name, f.Size, f.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return err
	}

	// Получаем фактический ID (может быть старым при конфликте).
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
		`SELECT id, path, name, size, created_at, deleted_at FROM files WHERE id=?`, id)
	return scanFile(row)
}

func (r *sqliteFileRepo) List(ctx context.Context) ([]*model.File, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, path, name, size, created_at, deleted_at FROM files WHERE deleted_at IS NULL ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

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

// ListDeleted возвращает удалённые файлы вместе с исходным URL из связанного задания.
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

// Delete — мягкое удаление: проставляет deleted_at, запись остаётся в БД.
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

func scanFile(s scanner) (*model.File, error) {
	var f model.File
	var createdAt string
	var deletedAt sql.NullString
	err := s.Scan(&f.ID, &f.Path, &f.Name, &f.Size, &createdAt, &deletedAt)
	if err != nil {
		return nil, err
	}
	f.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	if deletedAt.Valid && deletedAt.String != "" {
		t, _ := time.Parse(time.RFC3339Nano, deletedAt.String)
		f.DeletedAt = &t
	}
	return &f, nil
}
