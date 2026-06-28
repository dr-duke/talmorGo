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
