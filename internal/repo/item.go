package repo

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/dr-duke/talmorGo/internal/db"
	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/google/uuid"
)

type sqliteItemRepo struct {
	db *db.DB
}

func NewItemRepo(database *db.DB) ItemRepo {
	return &sqliteItemRepo{db: database}
}

const itemSelect = `
	SELECT id, COALESCE(job_id,''), kind, path, name, size, duration,
	       title, artist, album, year, genre,
	       created_at, COALESCE(deleted_at,''), COALESCE(lost_at,'')
	FROM items`

func (r *sqliteItemRepo) Create(ctx context.Context, item *model.Item) error {
	if item.ID == "" {
		item.ID = uuid.NewString()
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	// Файл по тому же пути появился снова: снимаем пометки об удалении и пропаже,
	// иначе восстановленный файл остаётся в медиатеке в статусе «удалён».
	// job_id при конфликте тоже переносится. Раньше он не обновлялся, и файл
	// оставался за первым заданием: второе получало статус «готово» с нулём
	// файлов и выпадало из обеих половин запроса медиатеки — строку нельзя было
	// ни увидеть, ни скрыть, ни удалить.
	//
	// Фактический ID возвращаем через RETURNING, а не отдельным SELECT по пути:
	// так значение приходит из той же операции и не зависит от чужих записей.
	return r.db.WriteRowContext(ctx,
		`INSERT INTO items (id, job_id, kind, path, name, size, duration,
		                    title, artist, album, year, genre, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(path) DO UPDATE SET
		     job_id=excluded.job_id,
		     name=excluded.name, size=excluded.size, duration=excluded.duration,
		     title=excluded.title, artist=excluded.artist, album=excluded.album,
		     year=excluded.year, genre=excluded.genre,
		     deleted_at=NULL, lost_at=NULL
		 RETURNING id`,
		item.ID, nullStr(item.JobID), item.Kind, item.Path, item.Name,
		item.Size, item.Duration,
		item.Meta.Title, item.Meta.Artist, item.Meta.Album, item.Meta.Year, item.Meta.Genre,
		item.CreatedAt.Format(time.RFC3339Nano),
	).Scan(&item.ID)
}

func (r *sqliteItemRepo) GetByID(ctx context.Context, id string) (*model.Item, error) {
	row := r.db.QueryRowContext(ctx, itemSelect+` WHERE id=?`, id)
	return scanItem(row)
}

func (r *sqliteItemRepo) ListAll(ctx context.Context) ([]*model.Item, error) {
	rows, err := r.db.QueryContext(ctx, itemSelect+` ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanItems(rows)
}

func (r *sqliteItemRepo) ListByJobID(ctx context.Context, jobID string) ([]*model.Item, error) {
	rows, err := r.db.QueryContext(ctx, itemSelect+` WHERE job_id=? ORDER BY created_at ASC`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanItems(rows)
}

func (r *sqliteItemRepo) DeleteAllByJobID(ctx context.Context, jobID string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM items WHERE job_id=?`, jobID)
	return err
}

func (r *sqliteItemRepo) ListDeleted(ctx context.Context) ([]*model.DeletedItem, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT i.id, i.name, i.kind, COALESCE(j.url,''), i.deleted_at
		FROM items i
		LEFT JOIN jobs j ON j.id = i.job_id
		WHERE i.deleted_at IS NOT NULL
		ORDER BY i.deleted_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.DeletedItem
	for rows.Next() {
		var d model.DeletedItem
		var deletedAt string
		if err := rows.Scan(&d.ID, &d.Name, &d.Kind, &d.OriginalURL, &deletedAt); err != nil {
			return nil, err
		}
		d.DeletedAt, _ = time.Parse(time.RFC3339Nano, deletedAt)
		out = append(out, &d)
	}
	return out, rows.Err()
}

func (r *sqliteItemRepo) KnownPaths(ctx context.Context) (map[string]bool, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT path, (deleted_at IS NULL AND lost_at IS NULL) FROM items`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	paths := make(map[string]bool)
	for rows.Next() {
		var p string
		var available bool
		if err := rows.Scan(&p, &available); err != nil {
			return nil, err
		}
		paths[p] = available
	}
	return paths, rows.Err()
}

func (r *sqliteItemRepo) RestoreByPath(ctx context.Context, path string, size int64) (bool, error) {
	res, err := r.db.ExecContext(ctx,
		`UPDATE items SET deleted_at=NULL, lost_at=NULL, size=? WHERE path=?`, size, path)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (r *sqliteItemRepo) PathsForCleanup(ctx context.Context) ([]string, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT i.path FROM items i
		JOIN jobs j ON j.id = i.job_id
		WHERE j.hidden=1 OR j.status='failed'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	return paths, rows.Err()
}

func (r *sqliteItemRepo) PruneLost(ctx context.Context) (int, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM items WHERE lost_at IS NOT NULL`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (r *sqliteItemRepo) Rename(ctx context.Context, id, newName, newPath string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE items SET name=?, path=? WHERE id=?`, newName, newPath, id)
	return err
}

func (r *sqliteItemRepo) SoftDelete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE items SET deleted_at=? WHERE id=? AND deleted_at IS NULL`,
		time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

func (r *sqliteItemRepo) MarkLost(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE items SET lost_at=? WHERE id=? AND lost_at IS NULL AND deleted_at IS NULL`,
		time.Now().UTC().Format(time.RFC3339Nano), id)
	return err
}

func (r *sqliteItemRepo) MarkFound(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `UPDATE items SET lost_at=NULL WHERE id=?`, id)
	return err
}

func (r *sqliteItemRepo) BulkUpdateMetaFields(ctx context.Context, ids []string, fields map[string]string) error {
	if len(ids) == 0 || len(fields) == 0 {
		return nil
	}
	// Белый список колонок — защита от SQL-инъекций через ключи map.
	var setClauses []string
	var args []any
	for _, col := range []string{"title", "artist", "album", "year", "genre"} {
		if val, ok := fields[col]; ok {
			setClauses = append(setClauses, col+"=?")
			args = append(args, val)
		}
	}
	if len(setClauses) == 0 {
		return nil
	}
	placeholders := make([]string, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args = append(args, id)
	}
	q := "UPDATE items SET " + strings.Join(setClauses, ", ") +
		" WHERE id IN (" + strings.Join(placeholders, ", ") + ")"
	_, err := r.db.ExecContext(ctx, q, args...)
	return err
}

func scanItem(s scanner) (*model.Item, error) {
	var item model.Item
	var createdAt, deletedAt, lostAt string
	err := s.Scan(
		&item.ID, &item.JobID, &item.Kind, &item.Path, &item.Name,
		&item.Size, &item.Duration,
		&item.Meta.Title, &item.Meta.Artist, &item.Meta.Album, &item.Meta.Year, &item.Meta.Genre,
		&createdAt, &deletedAt, &lostAt,
	)
	if err != nil {
		return nil, err
	}
	item.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	if deletedAt != "" {
		t, _ := time.Parse(time.RFC3339Nano, deletedAt)
		item.DeletedAt = &t
	}
	if lostAt != "" {
		t, _ := time.Parse(time.RFC3339Nano, lostAt)
		item.LostAt = &t
	}
	return &item, nil
}

func scanItems(rows *sql.Rows) ([]*model.Item, error) {
	var out []*model.Item
	for rows.Next() {
		item, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
