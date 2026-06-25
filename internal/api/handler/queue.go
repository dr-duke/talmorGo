package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/dr-duke/talmorGo/internal/config"
	"github.com/dr-duke/talmorGo/internal/downloader"
	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/dr-duke/talmorGo/internal/repo"
)

type Enqueuer interface {
	Enqueue()
	// CancelJob прерывает активно скачиваемый job. Возвращает true если job был running.
	CancelJob(jobID string) bool
}

type QueueHandler struct {
	Jobs repo.JobRepo
	Tags repo.TagRepo
	Pool Enqueuer
	Cfg  *config.Config
}

// Add добавляет URL в очередь немедленно, не блокируя ответ.
// Если URL — плейлист, разворачивание в отдельные job'ы происходит асинхронно.
func (h *QueueHandler) Add(w http.ResponseWriter, r *http.Request) {
	rawURL := ""
	ct := r.Header.Get("Content-Type")
	if ct == "application/json" {
		var body struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		rawURL = body.URL
	} else {
		r.ParseForm()
		rawURL = r.FormValue("url")
	}

	if rawURL == "" {
		http.Error(w, "url required", http.StatusBadRequest)
		return
	}
	if _, err := url.ParseRequestURI(rawURL); err != nil {
		http.Error(w, "invalid url", http.StatusBadRequest)
		return
	}

	// Создаём placeholder в статусе "checking" — воркер игнорирует этот статус.
	// Ответ отдаём немедленно; горутина проверяет плейлист и затем переводит
	// placeholder в pending (одиночное видео) или удаляет + создаёт отдельные jobs (плейлист).
	job := &model.Job{URL: rawURL, Status: model.JobChecking, Source: "web"}
	if err := h.Jobs.Create(r.Context(), job); err != nil {
		slog.Error("queue add", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	w.Header().Set("HX-Trigger", "mediaRefresh")
	w.WriteHeader(http.StatusNoContent)

	opts := downloader.Options{
		Binary:   h.Cfg.YtDlpBinary,
		Proxy:    h.Cfg.YtDlpProxy,
		MaxFiles: h.Cfg.YtDlpMaxFilesPerRequest,
		Timeout:  time.Duration(h.Cfg.YtDlpTimeout) * time.Second,
	}
	go h.tryExpandPlaylist(job.ID, rawURL, opts, "web", 0)
}

// tryExpandPlaylist проверяет URL на плейлист асинхронно.
// Placeholder создан в статусе "checking" — воркер его не трогает.
// Если одиночное видео: переводим в pending. Если плейлист: удаляем placeholder, создаём отдельные jobs.
func (h *QueueHandler) tryExpandPlaylist(placeholderID, rawURL string, opts downloader.Options, source string, chatID int64) {
	ctx := context.Background()
	info := downloader.FetchPlaylist(ctx, rawURL, opts)
	if info == nil {
		// Одиночное видео — переводим checking → pending, даём воркеру сигнал.
		if err := h.Jobs.ConfirmSingle(ctx, placeholderID); err != nil {
			slog.Error("queue: confirm single", "id", placeholderID, "err", err)
			return
		}
		h.Pool.Enqueue()
		return
	}

	// Плейлист: удаляем placeholder и создаём индивидуальные jobs.
	if err := h.Jobs.DeleteChecking(ctx, placeholderID); err != nil {
		slog.Error("queue: delete checking placeholder", "id", placeholderID, "err", err)
	}
	h.doCreatePlaylistJobs(ctx, info, source, chatID)
	h.Pool.Enqueue()
}

// createPlaylistJobs используется Telegram-ботом (синхронно, с контекстом запроса).
func (h *QueueHandler) createPlaylistJobs(r *http.Request, info *downloader.PlaylistInfo, source string, chatID int64) {
	h.doCreatePlaylistJobs(r.Context(), info, source, chatID)
}

// doCreatePlaylistJobs создаёт отдельные job'ы для каждого видео из плейлиста
// и назначает тег с названием плейлиста.
func (h *QueueHandler) doCreatePlaylistJobs(ctx context.Context, info *downloader.PlaylistInfo, source string, chatID int64) {
	var tagID string
	if info.PlaylistTitle != "" && h.Tags != nil {
		if tag, err := h.Tags.Upsert(ctx, info.PlaylistTitle); err == nil {
			tagID = tag.ID
		}
	}

	for _, entry := range info.Entries {
		job := &model.Job{
			URL:    entry.URL,
			Title:  entry.Title,
			Status: model.JobPending,
			Source: source,
			ChatID: chatID,
		}
		if err := h.Jobs.Create(ctx, job); err != nil {
			slog.Error("queue: create playlist job", "url", entry.URL, "err", err)
			continue
		}
		if tagID != "" {
			h.Tags.AddToJob(ctx, job.ID, tagID) //nolint:errcheck
		}
	}
}

// Delete отменяет задачу в любом статусе:
//   - running/retrying → убивает yt-dlp процесс (воркер сам проставит cancelled)
//   - pending/retrying  → мягкая отмена через БД (статус cancelled, запись остаётся)
func (h *QueueHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")

	// Сначала пробуем остановить активный download (running).
	if h.Pool.CancelJob(id) {
		w.Header().Set("HX-Trigger", "mediaRefresh")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Для pending / retrying — мягкая отмена через БД.
	if err := h.Jobs.Cancel(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("HX-Trigger", "mediaRefresh")
	w.WriteHeader(http.StatusNoContent)
}

// Retry переводит failed-задачу обратно в pending.
func (h *QueueHandler) Retry(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.Jobs.ResetFailed(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.Pool.Enqueue()
	w.Header().Set("HX-Trigger", "mediaRefresh")
	w.WriteHeader(http.StatusNoContent)
}
