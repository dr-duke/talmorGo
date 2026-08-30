package worker

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/dr-duke/talmorGo/internal/db"
	"github.com/dr-duke/talmorGo/internal/repo"
)

// TestDirScanner_SkipsStagingDir проверяет, что файлы внутри dot-каталога staging
// (.talmor-tmp) не импортируются — это и есть гарантия отсутствия гонки с закачкой.
func TestDirScanner_SkipsStagingDir(t *testing.T) {
	tmp := t.TempDir()
	database, err := db.Open(filepath.Join(tmp, "test.db"))
	if err != nil {
		t.Fatalf("db open: %v", err)
	}
	defer database.Close()

	jobs := repo.NewJobRepo(database)
	items := repo.NewItemRepo(database)

	// Готовый файл в корне — должен импортироваться.
	if err := os.WriteFile(filepath.Join(tmp, "ready.mp4"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Незавершённая закачка в staging — должна быть пропущена.
	staging := filepath.Join(tmp, ".talmor-tmp", "job-123")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "downloading.mp4"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewDirScanner(jobs, items, tmp, 0, NewInFlightPaths())
	s.scan(context.Background())

	all, err := items.KnownPaths(context.Background())
	if err != nil {
		t.Fatalf("known paths: %v", err)
	}
	if _, ok := all[filepath.Join(tmp, "ready.mp4")]; !ok {
		t.Error("ready.mp4 should have been imported")
	}
	if _, ok := all[filepath.Join(staging, "downloading.mp4")]; ok {
		t.Error("file inside .talmor-tmp must NOT be imported (race guard broken)")
	}
	if len(all) != 1 {
		t.Errorf("expected exactly 1 imported file, got %d", len(all))
	}
}

// TestDirScanner_SkipsInFlight проверяет вторичную защиту: путь в наборе inFlight
// (файл в момент перемещения из staging в OutputDir) не импортируется.
func TestDirScanner_SkipsInFlight(t *testing.T) {
	tmp := t.TempDir()
	database, err := db.Open(filepath.Join(tmp, "test.db"))
	if err != nil {
		t.Fatalf("db open: %v", err)
	}
	defer database.Close()

	jobs := repo.NewJobRepo(database)
	items := repo.NewItemRepo(database)

	moving := filepath.Join(tmp, "moving.mp4")
	if err := os.WriteFile(moving, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	inflight := NewInFlightPaths()
	inflight.Add(moving)

	s := NewDirScanner(jobs, items, tmp, 0, inflight)
	s.scan(context.Background())

	all, _ := items.KnownPaths(context.Background())
	if _, ok := all[moving]; ok {
		t.Error("in-flight file must NOT be imported")
	}
}

// Файл, вернувшийся на место удалённого, должен снова стать доступным, а не
// оставаться навсегда в статусе «удалён»: сканер раньше пропускал любой
// известный путь и запись не оживала.
func TestDirScanner_RestoresDeletedFile(t *testing.T) {
	tmp := t.TempDir()
	database, err := db.Open(filepath.Join(tmp, "test.db"))
	if err != nil {
		t.Fatalf("db open: %v", err)
	}
	defer database.Close()

	jobs := repo.NewJobRepo(database)
	items := repo.NewItemRepo(database)
	ctx := context.Background()

	path := filepath.Join(tmp, "clip.mp4")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := NewDirScanner(jobs, items, tmp, 0, NewInFlightPaths())
	s.scan(ctx)

	all, _ := items.ListAll(ctx)
	if len(all) != 1 {
		t.Fatalf("imported %d items, want 1", len(all))
	}
	itemID := all[0].ID

	// Пользователь удалил файл: запись остаётся, файла на диске нет.
	if err := items.SoftDelete(ctx, itemID); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	s.scan(ctx)

	// Файл вернулся на диск.
	if err := os.WriteFile(path, []byte("restored"), 0o644); err != nil {
		t.Fatal(err)
	}
	s.scan(ctx)

	got, err := items.GetByID(ctx, itemID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.IsAvailable() {
		t.Error("restored file must become available again")
	}
	if got.Size != 8 {
		t.Errorf("size = %d, want 8 (обновлён при восстановлении)", got.Size)
	}

	// Дубликат заводить нельзя.
	after, _ := items.ListAll(ctx)
	if len(after) != 1 {
		t.Errorf("items = %d, want 1 (создан дубликат)", len(after))
	}
	allJobs, _ := jobs.List(ctx, repo.JobFilter{})
	if len(allJobs) != 1 {
		t.Errorf("jobs = %d, want 1 (создано лишнее задание)", len(allJobs))
	}
}

func TestMoveFile(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.mp4")
	dst := filepath.Join(tmp, "sub", "dst.mp4")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := moveFile(src, dst); err != nil {
		t.Fatalf("moveFile: %v", err)
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Error("source should be gone after move")
	}
	b, err := os.ReadFile(dst)
	if err != nil || string(b) != "hello" {
		t.Errorf("dst content = %q, err=%v", b, err)
	}
}
