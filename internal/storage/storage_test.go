package storage

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestRename_RejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

	orig := filepath.Join(dir, "video.mp4")
	if err := os.WriteFile(orig, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	bad := []string{
		"../escape.mp4",
		"../../etc/passwd",
		"sub/dir.mp4",
		"..",
		".",
		"",
	}
	for _, name := range bad {
		if _, err := s.Rename(orig, name); !errors.Is(err, ErrInvalidName) {
			t.Errorf("Rename(%q): expected ErrInvalidName, got %v", name, err)
		}
	}

	// Файл не должен был сдвинуться.
	if _, err := os.Stat(orig); err != nil {
		t.Errorf("original file moved/removed by rejected rename: %v", err)
	}
}

func TestRename_AllowsCleanName(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

	orig := filepath.Join(dir, "video.mp4")
	if err := os.WriteFile(orig, []byte("data"), 0o644); err != nil {
		t.Fatal(err)
	}

	newPath, err := s.Rename(orig, "renamed.mp4")
	if err != nil {
		t.Fatalf("Rename clean name: %v", err)
	}
	if newPath != filepath.Join(dir, "renamed.mp4") {
		t.Errorf("unexpected new path: %s", newPath)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Errorf("renamed file not found: %v", err)
	}
}

func TestDelete_MissingFileIsNoError(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Delete(filepath.Join(t.TempDir(), "nope.mp4")); err != nil {
		t.Errorf("Delete of missing file should be nil, got %v", err)
	}
}

// TestRename_RefusesOccupiedName проверяет, что переименование в занятое имя не
// уничтожает чужой файл. os.Rename молча затирает то, что лежит по назначению:
// раньше одной неудачной операцией портились сразу два элемента — содержимое
// второго файла терялось, а отказ базы по уникальности приходил уже после.
func TestRename_RefusesOccupiedName(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

	a := filepath.Join(dir, "a.mp4")
	b := filepath.Join(dir, "b.mp4")
	if err := os.WriteFile(a, []byte("видео A"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("видео B"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Rename(a, "b.mp4"); err == nil {
		t.Fatal("переименование в занятое имя прошло — файл B был бы уничтожен")
	}

	got, err := os.ReadFile(b)
	if err != nil {
		t.Fatalf("файл B пропал: %v", err)
	}
	if string(got) != "видео B" {
		t.Errorf("файл B затёрт: %q", got)
	}
	if _, err := os.Stat(a); err != nil {
		t.Errorf("файл A не на месте после неудачного переименования: %v", err)
	}
}

// TestRename_SameNameIsNoop: переименование в собственное имя не должно
// упираться в проверку занятости.
func TestRename_SameNameIsNoop(t *testing.T) {
	dir := t.TempDir()
	s := New(dir)

	p := filepath.Join(dir, "clip.mp4")
	if err := os.WriteFile(p, []byte("данные"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Rename(p, "clip.mp4"); err != nil {
		t.Fatalf("переименование в то же имя отклонено: %v", err)
	}
	if data, err := os.ReadFile(p); err != nil || string(data) != "данные" {
		t.Errorf("файл повреждён: %q, %v", data, err)
	}
}
