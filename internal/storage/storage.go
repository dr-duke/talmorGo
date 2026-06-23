package storage

import (
	"fmt"
	"os"
	"path/filepath"
)

type Storage struct {
	root string
}

func New(root string) *Storage {
	return &Storage{root: root}
}

// Delete удаляет файл с диска.
func (s *Storage) Delete(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete %s: %w", path, err)
	}
	return nil
}

// Rename переименовывает файл на диске. Возвращает новый путь.
func (s *Storage) Rename(oldPath, newName string) (string, error) {
	dir := filepath.Dir(oldPath)
	newPath := filepath.Join(dir, newName)
	if err := os.Rename(oldPath, newPath); err != nil {
		return "", fmt.Errorf("rename %s → %s: %w", oldPath, newPath, err)
	}
	return newPath, nil
}
