package handler

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"

	"github.com/a-h/templ"
	"github.com/dr-duke/talmorGo/internal/audio"
	"github.com/dr-duke/talmorGo/internal/config"
	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/dr-duke/talmorGo/internal/playlist"
	"github.com/dr-duke/talmorGo/internal/repo"
	"github.com/dr-duke/talmorGo/internal/storage"
	"github.com/dr-duke/talmorGo/web/templates"
)

type MediaHandler struct {
	Jobs        repo.JobRepo
	Items       repo.ItemRepo
	Tags        repo.TagRepo
	Tokens      repo.TokenRepo
	Storage     *storage.Storage
	BaseURL     string
	Pool        Enqueuer
	Cfg         *config.Config
	Settings    repo.SettingsRepo
	Collections repo.CollectionRepo
	Expander    *playlist.Expander
}

// LibrarySidebar отдаёт HTML-фрагмент сайдбара с коллекциями (для обновления после изменения коллекций).
func (h *MediaHandler) LibrarySidebar(w http.ResponseWriter, r *http.Request) {
	cols, err := h.Collections.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	templ.Handler(templates.SidebarNav(cols)).ServeHTTP(w, r)
}

// LibraryItems — фрагмент списка элементов (HTMX, поддерживает фильтрацию).
func (h *MediaHandler) LibraryItems(w http.ResponseWriter, r *http.Request) {
	f := parseMediaFilter(r)
	items, err := h.Jobs.FilterMedia(r.Context(), f)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tagCounts, _ := h.Tags.ListWithCount(r.Context())
	templ.Handler(templates.ItemList(items, tagCounts, f)).ServeHTTP(w, r)
}

// TagsFragment отдаёт HTML-фрагмент облака тегов.
func (h *MediaHandler) TagsFragment(w http.ResponseWriter, r *http.Request) {
	tagCounts, err := h.Tags.ListWithCount(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	templ.Handler(templates.TagCloud(tagCounts)).ServeHTTP(w, r)
}

// BulkTag назначает тег набору заданий.
func (h *MediaHandler) BulkTag(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TagName string   `json:"tag"`
		JobIDs  []string `json:"job_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TagName == "" || len(body.JobIDs) == 0 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	tag, err := h.Tags.Upsert(r.Context(), body.TagName)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.Tags.BulkAddToJobs(r.Context(), tag.ID, body.JobIDs); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// BulkHide скрывает набор заданий.
func (h *MediaHandler) BulkHide(w http.ResponseWriter, r *http.Request) {
	var body struct {
		JobIDs []string `json:"job_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.JobIDs) == 0 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	for _, id := range body.JobIDs {
		h.Jobs.Hide(r.Context(), id) //nolint:errcheck
	}
	w.WriteHeader(http.StatusNoContent)
}

func parseMediaFilter(r *http.Request) model.MediaFilter {
	var tags []string
	for _, t := range r.URL.Query()["tag"] {
		if t != "" {
			tags = append(tags, t)
		}
	}
	return model.MediaFilter{
		Query: r.URL.Query().Get("q"),
		Kind:  r.URL.Query().Get("kind"),
		Tags:  tags,
	}
}

// Stream отдаёт медиаэлемент (с Range-поддержкой).
func (h *MediaHandler) Stream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	item, err := h.Items.GetByID(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !item.IsAvailable() {
		http.Error(w, "item not available", http.StatusGone)
		return
	}
	if r.URL.Query().Get("download") == "true" {
		w.Header().Set("Content-Disposition", `attachment; filename="`+item.Name+`"`)
	}
	http.ServeFile(w, r, item.Path)
}

// Delete — мягкое удаление: файл удаляется с диска, запись остаётся в БД.
func (h *MediaHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	item, err := h.Items.GetByID(r.Context(), id)
	if err != nil {
		http.Error(w, "item not found", http.StatusNotFound)
		return
	}
	h.Storage.Delete(item.Path) //nolint:errcheck
	if err := h.Items.SoftDelete(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("HX-Trigger", "mediaRefresh")
	w.WriteHeader(http.StatusNoContent)
}

// PurgeJob безвозвратно удаляет hidden job и все его элементы.
func (h *MediaHandler) PurgeJob(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	if items, err := h.Items.ListByJobID(r.Context(), jobID); err == nil {
		for _, item := range items {
			if item.IsAvailable() {
				h.Storage.Delete(item.Path) //nolint:errcheck
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

// Hide убирает job из основного интерфейса.
func (h *MediaHandler) Hide(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	if err := h.Jobs.Hide(r.Context(), jobID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Trigger", "mediaRefresh")
	w.WriteHeader(http.StatusNoContent)
}

// Unhide возвращает скрытую запись.
func (h *MediaHandler) Unhide(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	if err := h.Jobs.Unhide(r.Context(), jobID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Trigger", "mediaRefresh")
	w.WriteHeader(http.StatusNoContent)
}

// Rename переименовывает медиаэлемент на диске и в БД.
func (h *MediaHandler) Rename(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	item, err := h.Items.GetByID(r.Context(), id)
	if err != nil {
		http.Error(w, "item not found", http.StatusNotFound)
		return
	}
	newPath, err := h.Storage.Rename(item.Path, body.Name)
	if err != nil {
		if errors.Is(err, storage.ErrInvalidName) {
			http.Error(w, "invalid name", http.StatusBadRequest)
			return
		}
		http.Error(w, "rename failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := h.Items.Rename(r.Context(), id, body.Name, newPath); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Trigger", "mediaRefresh")
	w.WriteHeader(http.StatusNoContent)
}

// UpdateMeta обновляет аудио-метаданные элемента.
func (h *MediaHandler) UpdateMeta(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var meta model.AudioMeta
	if err := json.NewDecoder(r.Body).Decode(&meta); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := h.Items.UpdateMeta(r.Context(), id, meta); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Trigger", "mediaRefresh")
	w.WriteHeader(http.StatusNoContent)
}

// BulkMeta обновляет указанные аудио-теги для набора item ID.
// Поля из request.Fields записываются в БД и в файлы через ffmpeg (ремукс без перекодирования).
func (h *MediaHandler) BulkMeta(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ItemIDs []string          `json:"item_ids"`
		Fields  map[string]string `json:"fields"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if len(req.ItemIDs) == 0 || len(req.Fields) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	ctx := r.Context()

	if err := h.Items.BulkUpdateMetaFields(ctx, req.ItemIDs, req.Fields); err != nil {
		slog.Error("bulk meta: db update", "err", err)
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}

	for _, id := range req.ItemIDs {
		item, err := h.Items.GetByID(ctx, id)
		if err != nil || item.IsDeleted() || item.IsLost() || item.Kind != "audio" {
			continue
		}
		if err := audio.WriteTags(ctx, h.Cfg.FfmpegBinary, item.Path, req.Fields); err != nil {
			slog.Warn("bulk meta: write tags", "item_id", id, "err", err)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// CreateLink создаёт или возвращает presigned-ссылку на элемент.
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

// Redownload сбрасывает задание, удаляет все элементы и инициирует повторную загрузку.
func (h *MediaHandler) Redownload(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	job, err := h.Jobs.GetByID(r.Context(), jobID)
	if err != nil {
		http.Error(w, "job not found", http.StatusNotFound)
		return
	}
	if items, err := h.Items.ListByJobID(r.Context(), jobID); err == nil {
		for _, item := range items {
			h.Storage.Delete(item.Path) //nolint:errcheck
		}
	}
	if err := h.Items.DeleteAllByJobID(r.Context(), jobID); err != nil {
		slog.Warn("media: delete items for redownload", "job_id", jobID, "err", err)
	}
	if err := h.Jobs.Redownload(r.Context(), jobID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if h.Cfg != nil {
		opts := resolveExpanderOpts(r.Context(), h.Cfg, h.Settings)
		go func(id, rawURL string) {
			h.Expander.ResolvePlaceholder(context.Background(), id, rawURL, opts, "web", 0)
			h.Pool.Enqueue()
		}(jobID, job.URL)
	} else {
		h.Jobs.ConfirmSingle(context.Background(), jobID) //nolint:errcheck
		h.Pool.Enqueue()
	}

	w.Header().Set("HX-Trigger", "mediaRefresh")
	w.WriteHeader(http.StatusNoContent)
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
	w.Header().Set("HX-Trigger", `{"mediaRefresh":true,"tagsRefresh":true}`)
	w.WriteHeader(http.StatusNoContent)
}

// RemoveTag удаляет тег у задания.
func (h *MediaHandler) RemoveTag(w http.ResponseWriter, r *http.Request) {
	jobID := r.PathValue("id")
	tagName := r.PathValue("tag")
	if err := h.Tags.RemoveFromJob(r.Context(), jobID, tagName); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Trigger", `{"mediaRefresh":true,"tagsRefresh":true}`)
	w.WriteHeader(http.StatusNoContent)
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

// ListDeleted возвращает JSON-список soft-удалённых элементов.
func (h *MediaHandler) ListDeleted(w http.ResponseWriter, r *http.Request) {
	items, err := h.Items.ListDeleted(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(items) //nolint:errcheck
}

// ExtractAudio извлекает аудиодорожку из скачанного видеофайла.
func (h *MediaHandler) ExtractAudio(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	srcItem, err := h.Items.GetByID(r.Context(), id)
	if err != nil {
		http.Error(w, "item not found", http.StatusNotFound)
		return
	}
	if !srcItem.IsAvailable() {
		http.Error(w, "item not available", http.StatusGone)
		return
	}

	var meta model.AudioMeta
	if job, err := h.Jobs.GetByID(r.Context(), srcItem.JobID); err == nil {
		meta.Title = job.Title
		meta.Artist = job.Domain()
	}

	audioDir := h.Cfg.AudioDir()
	outPath, err := audio.Extract(r.Context(), h.Cfg.FfmpegBinary, srcItem.Path, audioDir, meta)
	if err != nil {
		slog.Error("extract audio", "item_id", id, "err", err)
		http.Error(w, "audio extraction failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	info, _ := os.Stat(outPath)
	var size int64
	if info != nil {
		size = info.Size()
	}

	audioItem := &model.Item{
		JobID: srcItem.JobID,
		Kind:  "audio",
		Path:  outPath,
		Name:  filepath.Base(outPath),
		Size:  size,
		Meta:  meta,
	}
	if err := h.Items.Create(r.Context(), audioItem); err != nil {
		slog.Error("extract audio: save item", "err", err)
	}

	slog.Info("audio extracted", "src", srcItem.Path, "dst", outPath)

	trigger, _ := json.Marshal(map[string]any{"showToast": filepath.Base(outPath), "mediaRefresh": true})
	w.Header().Set("HX-Trigger", string(trigger))
	w.WriteHeader(http.StatusNoContent)
}
