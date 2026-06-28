package repo

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/dr-duke/talmorGo/internal/model"
)

type sqliteCookieRepo struct {
	db *sql.DB
}

func NewCookieRepo(db *sql.DB) CookieRepo {
	return &sqliteCookieRepo{db: db}
}

func (r *sqliteCookieRepo) Upsert(ctx context.Context, domain, content string) error {
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO cookies (domain, content, updated_at)
		 VALUES (?, ?, ?)
		 ON CONFLICT(domain) DO UPDATE SET content=excluded.content, updated_at=excluded.updated_at`,
		domain, content, time.Now().UTC().Format(time.RFC3339),
	)
	return err
}

func (r *sqliteCookieRepo) List(ctx context.Context) ([]*model.CookieRecord, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT domain, content, updated_at FROM cookies ORDER BY domain`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*model.CookieRecord
	for rows.Next() {
		var c model.CookieRecord
		if err := rows.Scan(&c.Domain, &c.Content, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

func (r *sqliteCookieRepo) Delete(ctx context.Context, domain string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM cookies WHERE domain=?`, domain)
	return err
}

func (r *sqliteCookieRepo) MergeAll(ctx context.Context) (string, error) {
	records, err := r.List(ctx)
	if err != nil {
		return "", err
	}
	if len(records) == 0 {
		return "", nil
	}
	var sb strings.Builder
	sb.WriteString("# Netscape HTTP Cookie File\n")
	for _, rec := range records {
		content := strings.TrimSpace(rec.Content)
		if content != "" {
			sb.WriteString(content)
			sb.WriteByte('\n')
		}
	}
	return sb.String(), nil
}
