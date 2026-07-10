package worker

import (
	"context"
	"log/slog"
	"os"
	"time"

	"github.com/dr-duke/talmorGo/internal/repo"
)

// FileChecker периодически проверяет, что медиаэлементы присутствуют на диске.
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
	items, err := c.items.ListAll(ctx)
	if err != nil {
		slog.Error("checker: list items", "err", err)
		return
	}
	lost, found := 0, 0
	for _, item := range items {
		if item.IsDeleted() {
			continue
		}
		_, statErr := os.Stat(item.Path)
		missing := os.IsNotExist(statErr)

		if missing && !item.IsLost() {
			if err := c.items.MarkLost(ctx, item.ID); err != nil {
				slog.Error("checker: mark lost", "id", item.ID, "err", err)
			} else {
				lost++
			}
		} else if !missing && item.IsLost() {
			if err := c.items.MarkFound(ctx, item.ID); err != nil {
				slog.Error("checker: mark found", "id", item.ID, "err", err)
			} else {
				found++
			}
		}
	}
	if lost > 0 || found > 0 {
		slog.Info("checker: scan complete", "lost", lost, "found", found)
	}
}
