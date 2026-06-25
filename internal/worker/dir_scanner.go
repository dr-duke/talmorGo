package worker

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/dr-duke/talmorGo/internal/repo"
)

// mediaExtensions — расширения, которые DirScanner считает медиафайлами.
var mediaExtensions = map[string]bool{
	// video
	".mp4": true, ".mkv": true, ".avi": true, ".mov": true, ".webm": true,
	".flv": true, ".m4v": true, ".ts": true, ".wmv": true, ".m2ts": true,
	".vob": true, ".3gp": true,
	// audio
	".mp3": true, ".m4a": true, ".opus": true, ".flac": true, ".wav": true,
	".ogg": true, ".aac": true, ".wma": true,
}

// DirScanner периодически сканирует директорию скачивания (рекурсивно) и регистрирует
// медиафайлы, не привязанные ни к одному заданию, с source="filesystem".
type DirScanner struct {
	jobs     repo.JobRepo
	files    repo.FileRepo
	dir      string
	interval time.Duration
}

func NewDirScanner(jobs repo.JobRepo, files repo.FileRepo, dir string, intervalSec int) *DirScanner {
	return &DirScanner{
		jobs:     jobs,
		files:    files,
		dir:      dir,
		interval: time.Duration(intervalSec) * time.Second,
	}
}

// Start запускает периодическое сканирование. interval == 0 → выключено.
func (s *DirScanner) Start(ctx context.Context) {
	if s.interval == 0 {
		return
	}
	s.scan(ctx)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.scan(ctx)
		}
	}
}

func (s *DirScanner) scan(ctx context.Context) {
	known, err := s.files.AllPaths(ctx)
	if err != nil {
		slog.Error("dir-scanner: get known paths", "err", err)
		return
	}

	imported := 0
	walkErr := filepath.WalkDir(s.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // пропускаем недоступные пути
		}
		if d.IsDir() {
			if path != s.dir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		// пропускаем временные файлы yt-dlp в процессе скачивания
		if strings.HasSuffix(d.Name(), ".part") || strings.HasSuffix(d.Name(), ".ytdl") {
			return nil
		}
		if !mediaExtensions[strings.ToLower(filepath.Ext(d.Name()))] {
			return nil
		}
		if _, ok := known[path]; ok {
			return nil // уже известен
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		if importErr := s.importFile(ctx, path, d.Name(), info.Size()); importErr != nil {
			slog.Error("dir-scanner: import file", "path", path, "err", importErr)
		} else {
			imported++
			known[path] = struct{}{} // не импортировать повторно в одном проходе
		}
		return nil
	})
	if walkErr != nil && !os.IsNotExist(walkErr) {
		slog.Error("dir-scanner: walk", "dir", s.dir, "err", walkErr)
	}
	if imported > 0 {
		slog.Info("dir-scanner: imported", "files", imported, "dir", s.dir)
	}
}

// importFile создаёт synthetic job + file для найденного файла.
func (s *DirScanner) importFile(ctx context.Context, path, name string, size int64) error {
	job := &model.Job{
		URL:    "local",
		Title:  name,
		Status: model.JobImported,
		Source: "filesystem",
	}
	if err := s.jobs.Create(ctx, job); err != nil {
		return err
	}
	f := &model.File{
		JobID: job.ID,
		Path:  path,
		Name:  name,
		Size:  size,
	}
	if err := s.files.Create(ctx, f); err != nil {
		return err
	}
	job.FileID = f.ID
	return s.jobs.Update(ctx, job)
}
