package repo

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

type sqliteSettingsRepo struct {
	db *sql.DB
}

func NewSettingsRepo(db *sql.DB) SettingsRepo {
	return &sqliteSettingsRepo{db: db}
}

func (r *sqliteSettingsRepo) Get(ctx context.Context, key string) (string, error) {
	var value string
	err := r.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key=?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

func (r *sqliteSettingsRepo) Set(ctx context.Context, key, value string) error {
	if value == "" {
		_, err := r.db.ExecContext(ctx, `DELETE FROM settings WHERE key=?`, key)
		return err
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at
	`, key, value, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (r *sqliteSettingsRepo) All(ctx context.Context) (map[string]string, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		m[k] = v
	}
	return m, rows.Err()
}
