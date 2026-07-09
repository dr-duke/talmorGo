package repo

import (
	"context"
	"database/sql"
	"time"

	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/google/uuid"
)

type sqliteAudioRepo struct {
	db *sql.DB
}

func NewAudioRepo(db *sql.DB) AudioRepo {
	return &sqliteAudioRepo{db: db}
}

func (r *sqliteAudioRepo) Create(ctx context.Context, f *model.AudioFile) error {
	if f.ID == "" {
		f.ID = uuid.NewString()
	}
	if f.CreatedAt.IsZero() {
		f.CreatedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO audio_files (id, job_id, file_id, path, name, size, title, artist, album, year, genre, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
			title=excluded.title, artist=excluded.artist, album=excluded.album,
			year=excluded.year, genre=excluded.genre
	`,
		f.ID, f.JobID, nullStr(f.FileID), f.Path, f.Name, f.Size,
		f.Title, f.Artist, f.Album, f.Year, f.Genre,
		f.CreatedAt.Format(time.RFC3339Nano),
	)
	return err
}

func (r *sqliteAudioRepo) GetByID(ctx context.Context, id string) (*model.AudioFile, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, job_id, COALESCE(file_id,''), path, name, size,
		       title, artist, album, year, genre, created_at
		FROM audio_files WHERE id=?`, id)
	return scanAudio(row)
}

func (r *sqliteAudioRepo) List(ctx context.Context) ([]*model.AudioFile, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, job_id, COALESCE(file_id,''), path, name, size,
		       title, artist, album, year, genre, created_at
		FROM audio_files
		ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*model.AudioFile
	for rows.Next() {
		f, err := scanAudio(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (r *sqliteAudioRepo) UpdateMeta(ctx context.Context, id string, meta model.AudioMeta) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE audio_files SET title=?, artist=?, album=?, year=?, genre=? WHERE id=?`,
		meta.Title, meta.Artist, meta.Album, meta.Year, meta.Genre, id)
	return err
}

func (r *sqliteAudioRepo) Delete(ctx context.Context, id string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM audio_files WHERE id=?`, id)
	return err
}

type audioScanner interface {
	Scan(dest ...any) error
}

func scanAudio(s audioScanner) (*model.AudioFile, error) {
	var f model.AudioFile
	var createdAt string
	err := s.Scan(
		&f.ID, &f.JobID, &f.FileID, &f.Path, &f.Name, &f.Size,
		&f.Title, &f.Artist, &f.Album, &f.Year, &f.Genre, &createdAt,
	)
	if err != nil {
		return nil, err
	}
	f.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	return &f, nil
}
