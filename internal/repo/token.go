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

func (r *sqliteTokenRepo) Upsert(ctx context.Context, itemID string) (*model.Token, error) {
	existing, err := r.getByItemID(ctx, itemID)
	if err == nil {
		return existing, nil
	}
	if err != sql.ErrNoRows {
		return nil, err
	}

	t := &model.Token{
		Token:     uuid.NewString(),
		ItemID:    itemID,
		CreatedAt: time.Now().UTC(),
	}
	_, err = r.db.ExecContext(ctx,
		`INSERT INTO tokens (token, item_id, created_at) VALUES (?, ?, ?)`,
		t.Token, t.ItemID, t.CreatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return nil, err
	}
	return t, nil
}

func (r *sqliteTokenRepo) GetByToken(ctx context.Context, token string) (*model.Token, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT token, item_id, created_at FROM tokens WHERE token=?`, token)
	return scanToken(row)
}

func (r *sqliteTokenRepo) getByItemID(ctx context.Context, itemID string) (*model.Token, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT token, item_id, created_at FROM tokens WHERE item_id=?`, itemID)
	return scanToken(row)
}

func scanToken(s scanner) (*model.Token, error) {
	var t model.Token
	var createdAt string
	if err := s.Scan(&t.Token, &t.ItemID, &createdAt); err != nil {
		return nil, err
	}
	t.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	return &t, nil
}
