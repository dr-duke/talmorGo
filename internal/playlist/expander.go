// Package playlist содержит общую логику разворачивания URL в задания:
// проверку «плейлист или одиночное видео» и создание отдельных job на каждое видео.
// Используется веб-обработчиками (add/redownload) и Telegram-ботом, чтобы не дублировать код.
package playlist

import (
	"context"
	"log/slog"

	"github.com/dr-duke/talmorGo/internal/downloader"
	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/dr-duke/talmorGo/internal/repo"
	"github.com/dr-duke/talmorGo/internal/sse"
)

// Expander разворачивает плейлисты в отдельные задания.
type Expander struct {
	Jobs repo.JobRepo
	Tags repo.TagRepo
	Hub  *sse.Hub // опционально: уведомляет браузер после разворачивания
}

func New(jobs repo.JobRepo, tags repo.TagRepo) *Expander {
	return &Expander{Jobs: jobs, Tags: tags}
}

// CreateJobs создаёт одно pending-задание на каждое видео из плейлиста и
// помечает каждое тегом с названием плейлиста. Возвращает число созданных заданий.
func (e *Expander) CreateJobs(ctx context.Context, info *downloader.PlaylistInfo, source string, chatID int64) int {
	var tagID string
	if info.PlaylistTitle != "" && e.Tags != nil {
		if tag, err := e.Tags.Upsert(ctx, info.PlaylistTitle); err == nil {
			tagID = tag.ID
		}
	}

	created := 0
	for _, entry := range info.Entries {
		job := &model.Job{
			URL:    entry.URL,
			Title:  entry.Title,
			Status: model.JobPending,
			Source: source,
			ChatID: chatID,
		}
		if err := e.Jobs.Create(ctx, job); err != nil {
			slog.Error("playlist: create job", "url", entry.URL, "err", err)
			continue
		}
		if tagID != "" && e.Tags != nil {
			e.Tags.AddToJob(ctx, job.ID, tagID) //nolint:errcheck
		}
		created++
	}
	return created
}

// ResolvePlaceholder проверяет URL placeholder-задания (в статусе checking) на плейлист:
//   - одиночное видео → переводит placeholder checking → pending (ConfirmSingle);
//   - плейлист        → удаляет placeholder и создаёт отдельные задания (CreateJobs).
//
// Вызывается асинхронно: placeholder создан в статусе checking, который воркер игнорирует.
func (e *Expander) ResolvePlaceholder(ctx context.Context, placeholderID, rawURL string, opts downloader.Options, source string, chatID int64) {
	info := downloader.FetchPlaylist(ctx, rawURL, opts)
	if info == nil {
		// Одиночное видео — переводим checking → pending.
		if err := e.Jobs.ConfirmSingle(ctx, placeholderID); err != nil {
			slog.Error("playlist: confirm single", "id", placeholderID, "err", err)
		}
	} else {
		// Плейлист: удаляем placeholder и создаём индивидуальные задания.
		if err := e.Jobs.DeleteChecking(ctx, placeholderID); err != nil {
			slog.Error("playlist: delete checking placeholder", "id", placeholderID, "err", err)
		}
		e.CreateJobs(ctx, info, source, chatID)
	}
	if e.Hub != nil {
		e.Hub.Broadcast()
	}
}
