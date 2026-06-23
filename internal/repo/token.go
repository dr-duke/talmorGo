package repo

import (
	"context"
	"database/sql"
	"time"

	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/google/uuid"
)

type sqliteTokenRepo struct {
	db *sql.DB
}

func NewTokenRepo(db *sql.DB) TokenRepo {
	return &sqliteTokenRepo{db: db}
}

func (r *sqliteTokenRepo) Upsert(ctx context.Context, fileID string) (*model.Token, error) {
	// Сначала проверяем, есть ли уже токен для этого файла.
	existing, err := r.getByFileID(ctx, fileID)
	if err == nil {
		return existing, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}

	t := &model.Token{
		Token:     uuid.NewString(),
		FileID:    fileID,
		CreatedAt: time.Now().UTC(),
	}
	_, err = r.db.ExecContext(ctx,
		`INSERT INTO tokens (token, file_id, created_at) VALUES (?, ?, ?)`,
		t.Token, t.FileID, t.CreatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (r *sqliteTokenRepo) GetByToken(ctx context.Context, token string) (*model.Token, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT token, file_id, created_at FROM tokens WHERE token=?`, token)
	return scanToken(row)
}

func (r *sqliteTokenRepo) getByFileID(ctx context.Context, fileID string) (*model.Token, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT token, file_id, created_at FROM tokens WHERE file_id=?`, fileID)
	return scanToken(row)
}

func scanToken(s scanner) (*model.Token, error) {
	var t model.Token
	var createdAt string
	if err := s.Scan(&t.Token, &t.FileID, &createdAt); err != nil {
		return nil, err
	}
	t.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	return &t, nil
}
