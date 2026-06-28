package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/dr-duke/talmorGo/internal/config"
	"github.com/dr-duke/talmorGo/internal/downloader"
	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/dr-duke/talmorGo/internal/playlist"
	"github.com/dr-duke/talmorGo/internal/repo"
)

type Enqueuer interface {
	Enqueue()
	// CancelJob прерывает активно скачиваемый job. Возвращает true если job был running.
	CancelJob(jobID string) bool
}

type QueueHandler struct {
	Jobs     repo.JobRepo
	Tags     repo.TagRepo
	Pool     Enqueuer
	Cfg      *config.Config
	Expander *playlist.Expander
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

	cf := h.Cfg.CookiesFilePath()
	if _, err := os.Stat(cf); err != nil {
		cf = ""
	}
	opts := downloader.Options{
		Binary:      h.Cfg.YtDlpBinary,
		Proxy:       h.Cfg.YtDlpProxy,
		MaxFiles:    h.Cfg.YtDlpMaxFilesPerRequest,
		Timeout:     time.Duration(h.Cfg.YtDlpTimeout) * time.Second,
		CookiesFile: cf,
	}
	// Асинхронно проверяем плейлист и сигналим воркеру; placeholder в статусе checking.
	go func(id string) {
		h.Expander.ResolvePlaceholder(context.Background(), id, rawURL, opts, "web", 0)
		h.Pool.Enqueue()
	}(job.ID)
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
