package db

import (
	"context"
	"path/filepath"
	"testing"
)

// TestOpen_PragmasApplied проверяет, что параметры DSN действительно доходят до
// драйвера: раньше использовался синтаксис mattn/go-sqlite3, который modernc
// молча игнорирует, и база работала без WAL и без внешних ключей.
func TestOpen_PragmasApplied(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	ctx := context.Background()

	var journal string
	if err := database.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&journal); err != nil {
		t.Fatalf("journal_mode: %v", err)
	}
	if journal != "wal" {
		t.Errorf("journal_mode = %q, want wal", journal)
	}

	var fk int
	if err := database.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&fk); err != nil {
		t.Fatalf("foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}
}

// TestReadPool_IsReadOnly фиксирует контракт разделения пулов: читающее
// соединение не должно допускать запись.
func TestReadPool_IsReadOnly(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	ctx := context.Background()
	if _, err := database.ExecContext(ctx,
		`INSERT INTO jobs (id, url, status, source) VALUES ('j1','https://e.com','pending','web')`); err != nil {
		t.Fatalf("write via write pool: %v", err)
	}

	var got string
	if err := database.QueryRowContext(ctx, `SELECT url FROM jobs WHERE id='j1'`).Scan(&got); err != nil {
		t.Fatalf("read via read pool: %v", err)
	}
	if got != "https://e.com" {
		t.Errorf("url = %q", got)
	}

	// Запись через read-пул должна отклоняться (query_only).
	var one int
	err = database.read.QueryRowContext(ctx,
		`UPDATE jobs SET url='x' WHERE id='j1' RETURNING 1`).Scan(&one)
	if err == nil {
		t.Error("write through read pool must fail (query_only not enforced)")
	}
}

// TestForeignKeys_CascadeOnJobDelete проверяет, что включённые внешние ключи
// действительно каскадно удаляют зависимые строки.
func TestForeignKeys_CascadeOnJobDelete(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	ctx := context.Background()
	if _, err := database.ExecContext(ctx,
		`INSERT INTO jobs (id, url, status, source) VALUES ('j1','https://e.com','done','web')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx,
		`INSERT INTO items (id, job_id, kind, path, name) VALUES ('i1','j1','video','/data/a.mp4','a.mp4')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `DELETE FROM jobs WHERE id='j1'`); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := database.QueryRowContext(ctx, `SELECT COUNT(*) FROM items WHERE job_id='j1'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("items after job delete = %d, want 0 (cascade not working)", n)
	}
}

// TestInsertViolatingForeignKey фиксирует, что сироты больше не создаются.
func TestInsertViolatingForeignKey(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer database.Close()

	_, err = database.ExecContext(context.Background(),
		`INSERT INTO items (id, job_id, kind, path, name) VALUES ('i1','ghost','video','/data/a.mp4','a.mp4')`)
	if err == nil {
		t.Error("insert with dangling job_id must fail")
	}
}
