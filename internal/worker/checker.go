package worker

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/dr-duke/talmorGo/internal/repo"
)

// FileChecker периодически проверяет, что скачанные файлы присутствуют на диске.
// Если файл исчез — ставит статус lost; если появился снова — сбрасывает его.
type FileChecker struct {
	files    repo.FileRepo
	interval time.Duration
}

func NewFileChecker(files repo.FileRepo, intervalSec int) *FileChecker {
	return &FileChecker{
		files:    files,
		interval: time.Duration(intervalSec) * time.Second,
	}
}

func (c *FileChecker) Start(ctx context.Context) {
	// Первая проверка сразу после старта.
	c.check(ctx)

	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.check(ctx)
		}
	}
}

func (c *FileChecker) check(ctx context.Context) {
	files, err := c.files.ListAll(ctx)
	if err != nil {
		slog.Error("checker: list files", "err", err)
		return
	}
	lost, found := 0, 0
	for _, f := range files {
		if f.IsDeleted() {
			continue
		}
		_, statErr := os.Stat(f.Path)
		missing := os.IsNotExist(statErr)

		if missing && !f.IsLost() {
			if err := c.files.MarkLost(ctx, f.ID); err != nil {
				slog.Error("checker: mark lost", "id", f.ID, "err", err)
			} else {
				lost++
			}
		} else if !missing && f.IsLost() {
			if err := c.files.MarkFound(ctx, f.ID); err != nil {
				slog.Error("checker: mark found", "id", f.ID, "err", err)
			} else {
				found++
			}
		}
	}
	if lost > 0 || found > 0 {
		slog.Info("checker: scan complete", "lost", lost, "found", found)
	}
}
