package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/a-h/templ"
	"github.com/dr-duke/talmorGo/internal/config"
	"github.com/dr-duke/talmorGo/internal/downloader"
	"github.com/dr-duke/talmorGo/internal/repo"
	"github.com/dr-duke/talmorGo/internal/storage"
	"github.com/dr-duke/talmorGo/web/templates"
)

// MediaHandler обслуживает объединённый список файлов+очереди и операции над ними.
type MediaHandler struct {
	Jobs    repo.JobRepo
	Files   repo.FileRepo
	Tags    repo.TagRepo
	Tokens  repo.TokenRepo
	Storage *storage.Storage
	BaseURL string
	Pool    Enqueuer
	Cfg     *config.Config
}

// Page — полная страница (tab switching).
func (h *MediaHandler) Page(w http.ResponseWriter, r *http.Request) {
	items, err := h.Jobs.ListMedia(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tags, _ := h.Tags.ListAll(r.Context())
	templ.Handler(templates.MediaTab(items, tags)).ServeHTTP(w, r)
}

// List — фрагмент для HTMX polling.
func (h *MediaHandler) List(w http.ResponseWriter, r *http.Request) {
	items, err := h.Jobs.ListMedia(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tags, _ := h.Tags.ListAll(r.Context())
	templ.Handler(templates.MediaList(items, tags)).ServeHTTP(w, r)
}

// Stream отдаёт файл (с Range-поддержкой) если он доступен.
func (h *MediaHandler) Stream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	f, err := h.Files.GetByID(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !f.IsAvailable() {
		http.Error(w, "File not available", http.StatusGone)
		return
	}
	if r.URL.Query().Get("download") == "true" {
		w.Header().Set("Content-Disposition", `attachment; filename="`+f.Name+`"`)
	}
	http.ServeFile(w, r, f.Path)
}

// Delete — мягкое удаление доступного файла.
// Если файл уже deleted или missing — скрывает job (hidden=1).
func (h *MediaHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	f, err := h.Files.GetByID(r.Context(), id)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}

	if f.IsDeleted() || f.IsLost() {
		// Повторное нажатие delete на deleted/missing → скрыть job.
		if err := h.Jobs.Hide(r.Context(), f.JobID); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.List(w, r)
		return
	}

	h.Storage.Delete(f.Path) //nolint:errcheck
	if err := h.Files.Delete(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.List(w, r)
}

// PurgeJob безвозвратно удаляет hidden job, его файлы из БД и с диска.
func (h *MediaHandler) PurgeJob(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")

	// Удаляем физические файлы с диска.
	if files, err := h.Files.ListByJobID(r.Context(), jobID); err == nil {
		for _, f := range files {
			if f.IsAvailable() {
				h.Storage.Delete(f.Path) //nolint:errcheck
			}
		}
	}

	if err := h.Jobs.Purge(r.Context(), jobID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Trigger", "mediaRefresh")
	w.WriteHeader(http.StatusNoContent)
}

// Hide скрывает job из основного интерфейса (только для hidden jobs в «скрытых»).
func (h *MediaHandler) Hide(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	if err := h.Jobs.Hide(r.Context(), jobID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Trigger", "mediaRefresh")
	w.WriteHeader(http.StatusNoContent)
}

// Rename переименовывает файл на диске и в БД.
func (h *MediaHandler) Rename(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	f, err := h.Files.GetByID(r.Context(), id)
	if err != nil {
		http.Error(w, "file not found", http.StatusNotFound)
		return
	}
	// Storage.Rename санирует имя (защита от path traversal) и переименовывает на диске.
	newPath, err := h.Storage.Rename(f.Path, body.Name)
	if err != nil {
		if errors.Is(err, storage.ErrInvalidName) {
			http.Error(w, "invalid file name", http.StatusBadRequest)
			return
		}
		http.Error(w, "rename failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.Files.Rename(r.Context(), id, body.Name, newPath); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.List(w, r)
}

// CreateLink создаёт или возвращает presigned-ссылку на файл.
func (h *MediaHandler) CreateLink(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tok, err := h.Tokens.Upsert(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"url": h.BaseURL + "/f/" + tok.Token}) //nolint:errcheck
}

// Redownload сбрасывает задание в checking, удаляет все файлы и запускает проверку плейлиста.
func (h *MediaHandler) Redownload(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	job, err := h.Jobs.GetByID(r.Context(), jobID)
	if err != nil {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}

	// Удаляем физические файлы с диска.
	if files, err := h.Files.ListByJobID(r.Context(), jobID); err == nil {
		for _, f := range files {
			h.Storage.Delete(f.Path) //nolint:errcheck
		}
	}
	if err := h.Files.DeleteAllByJobID(r.Context(), jobID); err != nil {
		slog.Warn("media: delete files for redownload", "job_id", jobID, "err", err)
	}
	if err := h.Jobs.Redownload(r.Context(), jobID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Запускаем проверку плейлиста в фоне (аналогично QueueHandler.Add).
	if h.Cfg != nil {
		opts := downloader.Options{
			Binary:   h.Cfg.YtDlpBinary,
			Proxy:    h.Cfg.YtDlpProxy,
			MaxFiles: h.Cfg.YtDlpMaxFilesPerRequest,
			Timeout:  time.Duration(h.Cfg.YtDlpTimeout) * time.Second,
		}
		go h.tryExpandPlaylist(jobID, job.URL, opts)
	} else {
		// Нет конфига — сразу переводим в pending.
		h.Jobs.ConfirmSingle(context.Background(), jobID) //nolint:errcheck
		h.Pool.Enqueue()
	}

	h.List(w, r)
}

func (h *MediaHandler) tryExpandPlaylist(placeholderID, rawURL string, opts downloader.Options) {
	ctx := context.Background()
	info := downloader.FetchPlaylist(ctx, rawURL, opts)
	if info == nil {
		if err := h.Jobs.ConfirmSingle(ctx, placeholderID); err != nil {
			slog.Error("media redownload: confirm single", "id", placeholderID, "err", err)
			return
		}
		h.Pool.Enqueue()
		return
	}
	if err := h.Jobs.DeleteChecking(ctx, placeholderID); err != nil {
		slog.Error("media redownload: delete checking", "id", placeholderID, "err", err)
	}
	// Переиспользуем логику QueueHandler.
	qh := &QueueHandler{Jobs: h.Jobs, Tags: h.Tags, Pool: h.Pool, Cfg: h.Cfg}
	qh.doCreatePlaylistJobs(ctx, info, "web", 0)
	h.Pool.Enqueue()
}

// AddTag добавляет тег к заданию.
func (h *MediaHandler) AddTag(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	tag, err := h.Tags.Upsert(r.Context(), body.Name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.Tags.AddToJob(r.Context(), jobID, tag.ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.List(w, r)
}

// RemoveTag удаляет тег у задания.
func (h *MediaHandler) RemoveTag(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	tagName := r.PathValue("tag")
	if err := h.Tags.RemoveFromJob(r.Context(), jobID, tagName); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.List(w, r)
}

// Log возвращает plain-text лог последней попытки скачивания.
func (h *MediaHandler) Log(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	log, err := h.Jobs.GetLog(r.Context(), jobID)
	if err != nil || log == "" {
		http.Error(w, "log not available", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(log)) //nolint:errcheck
}

// ListDeleted возвращает JSON-список soft-удалённых файлов.
func (h *MediaHandler) ListDeleted(w http.ResponseWriter, r *http.Request) {
	files, err := h.Files.ListDeleted(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(files) //nolint:errcheck
}
