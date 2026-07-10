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

func kindFromExt(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mp3", ".m4a", ".opus", ".flac", ".wav", ".ogg", ".aac", ".wma":
		return "audio"
	default:
		return "video"
	}
}

type DirScanner struct {
	jobs     repo.JobRepo
	items    repo.ItemRepo
	dir      string
	interval time.Duration
	inFlight *InFlightPaths
}

func NewDirScanner(jobs repo.JobRepo, items repo.ItemRepo, dir string, intervalSec int, inFlight *InFlightPaths) *DirScanner {
	return &DirScanner{
		jobs:     jobs,
		items:    items,
		dir:      dir,
		interval: time.Duration(intervalSec) * time.Second,
		inFlight: inFlight,
	}
}

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
	known, err := s.items.AllPaths(ctx)
	if err != nil {
		slog.Error("dir-scanner: get known paths", "err", err)
		return
	}

	imported := 0
	walkErr := filepath.WalkDir(s.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
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
		if strings.HasSuffix(d.Name(), ".part") || strings.HasSuffix(d.Name(), ".ytdl") {
			return nil
		}
		if !mediaExtensions[strings.ToLower(filepath.Ext(d.Name()))] {
			return nil
		}
		if _, ok := known[path]; ok {
			return nil
		}
		if s.inFlight != nil && s.inFlight.Contains(path) {
			return nil
		}
		info, infoErr := d.Info()
		if infoErr != nil {
			return nil
		}
		if importErr := s.importFile(ctx, path, d.Name(), info.Size()); importErr != nil {
			slog.Error("dir-scanner: import file", "path", path, "err", importErr)
		} else {
			imported++
			known[path] = struct{}{}
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
	item := &model.Item{
		JobID: job.ID,
		Kind:  kindFromExt(name),
		Path:  path,
		Name:  name,
		Size:  size,
	}
	return s.items.Create(ctx, item)
}
