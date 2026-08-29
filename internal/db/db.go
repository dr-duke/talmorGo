package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"runtime"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// DB — пара пулов соединений над одним файлом SQLite.
//
// SQLite допускает только одного писателя, поэтому запись идёт через единственное
// соединение. В режиме WAL читатели писателю не мешают, поэтому чтение вынесено
// в отдельный пул: запросы UI, SSE и фоновых сканеров больше не выстраиваются
// в очередь за загрузками.
//
// Выбор пула определяется методом: Query/QueryRow — чтение, Exec/BeginTx — запись.
// Для запросов, которые пишут и возвращают строку (UPDATE … RETURNING),
// есть отдельный WriteRowContext.
type DB struct {
	write *sql.DB
	read  *sql.DB
	// shared=true, если чтение и запись идут через один пул (in-memory база).
	shared bool
}

// maxReadConns — размер пула читателей.
func maxReadConns() int {
	n := runtime.NumCPU()
	if n < 2 {
		return 2
	}
	if n > 8 {
		return 8
	}
	return n
}

// isMemory сообщает, что база живёт в памяти. Для такой базы отдельный пул
// читателей открыл бы другую, пустую базу, поэтому пул здесь один.
func isMemory(path string) bool {
	return path == ":memory:" || strings.Contains(path, "mode=memory")
}

func dsn(path string, readOnly bool) string {
	// Параметры modernc.org/sqlite задаются через _pragma; синтаксис mattn/go-sqlite3
	// (_journal_mode=WAL и т.п.) этим драйвером не распознаётся.
	pragmas := []string{
		"_pragma=busy_timeout(5000)",
		"_pragma=journal_mode(WAL)",
		"_pragma=foreign_keys(on)",
	}
	if readOnly {
		pragmas = append(pragmas, "_pragma=query_only(true)")
	}
	return fmt.Sprintf("file:%s?%s", path, strings.Join(pragmas, "&"))
}

func Open(path string) (*DB, error) {
	write, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	write.SetMaxOpenConns(1)

	if err := migrate(write); err != nil {
		write.Close() //nolint:errcheck
		return nil, fmt.Errorf("migrate: %w", err)
	}

	if isMemory(path) {
		return &DB{write: write, read: write, shared: true}, nil
	}

	read, err := sql.Open("sqlite", dsn(path, true))
	if err != nil {
		write.Close() //nolint:errcheck
		return nil, fmt.Errorf("open sqlite (read): %w", err)
	}
	read.SetMaxOpenConns(maxReadConns())

	return &DB{write: write, read: read}, nil
}

// QueryContext выполняет читающий запрос (пул читателей).
func (d *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return d.read.QueryContext(ctx, query, args...)
}

// QueryRowContext выполняет читающий запрос на одну строку (пул читателей).
func (d *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return d.read.QueryRowContext(ctx, query, args...)
}

// ExecContext выполняет запись (единственное пишущее соединение).
func (d *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return d.write.ExecContext(ctx, query, args...)
}

// WriteRowContext выполняет пишущий запрос, возвращающий строку (UPDATE … RETURNING).
func (d *DB) WriteRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return d.write.QueryRowContext(ctx, query, args...)
}

// BeginTx открывает транзакцию на пишущем соединении.
func (d *DB) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return d.write.BeginTx(ctx, opts)
}

func (d *DB) Close() error {
	err := d.write.Close()
	if !d.shared {
		if rerr := d.read.Close(); err == nil {
			err = rerr
		}
	}
	return err
}

func migrate(db *sql.DB) error {
	// Создаём таблицу отслеживания применённых миграций, если её нет.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})

	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		name := e.Name()

		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name=?`, name).Scan(&count); err != nil {
			return fmt.Errorf("check migration %s: %w", name, err)
		}
		if count > 0 {
			continue // уже применена
		}

		data, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			return fmt.Errorf("read %s: %w", name, err)
		}
		if _, err := db.Exec(string(data)); err != nil {
			return fmt.Errorf("exec %s: %w", name, err)
		}
		if _, err := db.Exec(`INSERT INTO schema_migrations (name) VALUES (?)`, name); err != nil {
			return fmt.Errorf("record migration %s: %w", name, err)
		}
	}
	return nil
}
