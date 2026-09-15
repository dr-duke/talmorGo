package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Storage struct {
	root string
}

func New(root string) *Storage {
	return &Storage{root: root}
}

// ErrInvalidName возвращается, когда новое имя файла содержит разделители пути
// или попытку выхода за пределы каталога (path traversal).
var ErrInvalidName = fmt.Errorf("invalid file name")

// Delete удаляет файл с диска. Отсутствие файла не считается ошибкой.
func (s *Storage) Delete(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("delete %s: %w", path, err)
	}
	return nil
}

// Rename переименовывает файл в пределах его текущего каталога. Возвращает новый путь.
// newName должно быть «чистым» именем файла без разделителей пути — иначе ErrInvalidName.
// Это защищает от path traversal (напр. "../../etc/passwd").
func (s *Storage) Rename(oldPath, newName string) (string, error) {
	if err := validateName(newName); err != nil {
		return "", err
	}
	newPath := filepath.Join(filepath.Dir(oldPath), newName)
	if newPath == oldPath {
		return newPath, nil
	}
	// Имя занимаем атомарным созданием файла. os.Rename молча затирает то, что
	// лежит по назначению: переименование одного файла в имя другого
	// уничтожало второй, а отказ базы по уникальности пути приходил уже после
	// необратимой правки диска — портились сразу два элемента.
	f, err := os.OpenFile(newPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return "", fmt.Errorf("файл с именем %q уже есть в медиатеке", newName)
		}
		return "", fmt.Errorf("rename %s → %s: %w", oldPath, newPath, err)
	}
	f.Close() //nolint:errcheck // заглушку тут же заменит перенос

	if err := os.Rename(oldPath, newPath); err != nil {
		os.Remove(newPath) //nolint:errcheck // снимаем заглушку
		return "", fmt.Errorf("rename %s → %s: %w", oldPath, newPath, err)
	}
	return newPath, nil
}

// validateName проверяет, что имя — это одиночный сегмент пути без traversal.
func validateName(name string) error {
	if name == "" || name == "." || name == ".." {
		return ErrInvalidName
	}
	if strings.ContainsRune(name, '/') || strings.ContainsRune(name, os.PathSeparator) {
		return ErrInvalidName
	}
	// filepath.Base схлопывает любые хитрости; если результат отличается — имя небезопасно.
	if filepath.Base(name) != name {
		return ErrInvalidName
	}
	return nil
}
