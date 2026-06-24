package handler

import (
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
}

type QueueHandler struct {
	Jobs repo.JobRepo
	Tags repo.TagRepo
	Pool Enqueuer
	Cfg  *config.Config
}

// Add добавляет URL(ы) в очередь. Если URL — плейлист, разворачивает его
// в отдельные job'ы через yt-dlp --flat-playlist и автоматически назначает тег.
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

	opts := downloader.Options{
		Binary:   h.Cfg.YtDlpBinary,
		Proxy:    h.Cfg.YtDlpProxy,
		MaxFiles: h.Cfg.YtDlpMaxFilesPerRequest,
		Timeout:  time.Duration(h.Cfg.YtDlpTimeout) * time.Second,
	}

	if info := downloader.FetchPlaylist(r.Context(), rawURL, opts); info != nil {
		h.createPlaylistJobs(r, info, "web", 0)
	} else {
		job := &model.Job{URL: rawURL, Status: model.JobPending, Source: "web"}
		if err := h.Jobs.Create(r.Context(), job); err != nil {
			slog.Error("queue add", "err", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
	}

	h.Pool.Enqueue()
	w.Header().Set("HX-Trigger", "mediaRefresh")
	w.WriteHeader(http.StatusNoContent)
}

// createPlaylistJobs создаёт отдельные job'ы для каждого видео из плейлиста
// и назначает тег с названием плейлиста.
func (h *QueueHandler) createPlaylistJobs(r *http.Request, info *downloader.PlaylistInfo, source string, chatID int64) {
	ctx := r.Context()

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

// Delete отменяет pending-задачу.
func (h *QueueHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.Jobs.Delete(r.Context(), id); err != nil {
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
