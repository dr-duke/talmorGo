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

	all, err := items.AllPaths(context.Background())
	if err != nil {
		t.Fatalf("all paths: %v", err)
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

	all, _ := items.AllPaths(context.Background())
	if _, ok := all[moving]; ok {
		t.Error("in-flight file must NOT be imported")
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
