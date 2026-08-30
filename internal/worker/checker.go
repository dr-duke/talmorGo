package worker

import (
	"context"
	"log/slog"
	"time"

	"github.com/dr-duke/talmorGo/internal/filecheck"
	"github.com/dr-duke/talmorGo/internal/repo"
)

// FileChecker периодически проверяет, что медиаэлементы присутствуют на диске.
// Сама сверка живёт в пакете filecheck — её же вызывает операция reindex.
type FileChecker struct {
	items    repo.ItemRepo
	interval time.Duration
}

func NewFileChecker(items repo.ItemRepo, intervalSec int) *FileChecker {
	return &FileChecker{
		items:    items,
		interval: time.Duration(intervalSec) * time.Second,
	}
}

func (c *FileChecker) Start(ctx context.Context) {
	if c.interval <= 0 {
		slog.Info("checker: disabled")
		return
	}
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
	lost, found, err := filecheck.Run(ctx, c.items)
	if err != nil {
		slog.Error("checker: scan", "err", err)
		return
	}
	if lost > 0 || found > 0 {
		slog.Info("checker: scan complete", "lost", lost, "found", found)
	}
}
