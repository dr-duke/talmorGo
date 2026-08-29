package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"github.com/a-h/templ"
	"github.com/dr-duke/talmorGo/internal/config"
	"github.com/dr-duke/talmorGo/internal/library"
	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/dr-duke/talmorGo/internal/storage"
	"github.com/dr-duke/talmorGo/web/templates"
)

// MediaHandler — адаптер HTTP → library.Service. Доменных правил здесь нет.
type MediaHandler struct {
	Lib *library.Service
	Cfg *config.Config
}

// LibrarySidebar отдаёт фрагмент сайдбара с коллекциями.
func (h *MediaHandler) LibrarySidebar(w http.ResponseWriter, r *http.Request) {
	cols, err := h.Lib.ListCollections(r.Context())
	if err != nil {
		httpError(w, err)
		return
	}
	templ.Handler(templates.SidebarNav(cols)).ServeHTTP(w, r)
}

// LibraryItems — фрагмент списка. С параметром rows=1 возвращает только строки
// следующей страницы (догрузка по кнопке «Показать ещё»).
func (h *MediaHandler) LibraryItems(w http.ResponseWriter, r *http.Request) {
	page, err := h.Lib.Media(r.Context(), parseMediaFilter(r))
	if err != nil {
		httpError(w, err)
		return
	}
	if r.URL.Query().Get("rows") == "1" {
		templ.Handler(templates.ItemRows(page)).ServeHTTP(w, r)
		return
	}
	templ.Handler(templates.ItemList(page)).ServeHTTP(w, r)
}

// TagsFragment отдаёт облако тегов с учётом текущего фильтра.
func (h *MediaHandler) TagsFragment(w http.ResponseWriter, r *http.Request) {
	f := parseMediaFilter(r)
	tagCounts, err := h.Lib.TagCloud(r.Context(), f)
	if err != nil {
		httpError(w, err)
		return
	}
	activeTag := ""
	if len(f.Tags) > 0 {
		activeTag = f.Tags[0]
	}
	templ.Handler(templates.TagCloud(tagCounts, activeTag)).ServeHTTP(w, r)
}

// PlaylistItems возвращает JSON-плейлист по текущему фильтру.
func (h *MediaHandler) PlaylistItems(w http.ResponseWriter, r *http.Request) {
	basePath := strings.TrimRight(h.Cfg.BasePath, "/")
	entries, err := h.Lib.Playlist(r.Context(), parseMediaFilter(r), basePath)
	if err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, entries)
}

func parseMediaFilter(r *http.Request) model.MediaFilter {
	q := r.URL.Query()
	var tags []string
	for _, t := range q["tag"] {
		if t != "" {
			tags = append(tags, t)
		}
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	if offset < 0 {
		offset = 0
	}
	return model.MediaFilter{
		Query:  q.Get("q"),
		Kind:   q.Get("kind"),
		Tags:   tags,
		Offset: offset,
	}
}

// Stream отдаёт медиаэлемент (с поддержкой Range).
func (h *MediaHandler) Stream(w http.ResponseWriter, r *http.Request) {
	item, err := h.Lib.AvailableItem(r.Context(), r.PathValue("id"))
	if err != nil {
		notFoundOrGone(w, r, err)
		return
	}
	if r.URL.Query().Get("download") == "true" {
		setAttachment(w, item.Name)
	}
	http.ServeFile(w, r, item.Path)
}

// setAttachment выставляет заголовок скачивания с корректным экранированием:
// имя файла задаётся пользователем и может содержать кавычки или юникод.
func setAttachment(w http.ResponseWriter, name string) {
	w.Header().Set("Content-Disposition",
		mime.FormatMediaType("attachment", map[string]string{"filename": name}))
}

func (h *MediaHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.Lib.DeleteItem(r.Context(), r.PathValue("id")); err != nil {
		httpError(w, err)
		return
	}
	refresh(w, "mediaRefresh")
}

func (h *MediaHandler) Rename(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := h.Lib.RenameItem(r.Context(), r.PathValue("id"), body.Name); err != nil {
		if errors.Is(err, storage.ErrInvalidName) {
			http.Error(w, "invalid name", http.StatusBadRequest)
			return
		}
		httpError(w, err)
		return
	}
	refresh(w, "mediaRefresh")
}

func (h *MediaHandler) PurgeJob(w http.ResponseWriter, r *http.Request) {
	if err := h.Lib.PurgeJob(r.Context(), r.PathValue("id")); err != nil {
		httpError(w, err)
		return
	}
	refresh(w, "mediaRefresh")
}

func (h *MediaHandler) Hide(w http.ResponseWriter, r *http.Request) {
	if err := h.Lib.HideJob(r.Context(), r.PathValue("id")); err != nil {
		httpError(w, err)
		return
	}
	refresh(w, "mediaRefresh")
}

func (h *MediaHandler) Unhide(w http.ResponseWriter, r *http.Request) {
	if err := h.Lib.UnhideJob(r.Context(), r.PathValue("id")); err != nil {
		httpError(w, err)
		return
	}
	refresh(w, "mediaRefresh")
}

func (h *MediaHandler) AddTag(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := h.Lib.AddTag(r.Context(), r.PathValue("id"), body.Name); err != nil {
		httpError(w, err)
		return
	}
	refresh(w, "mediaRefresh", "tagsRefresh")
}

func (h *MediaHandler) RemoveTag(w http.ResponseWriter, r *http.Request) {
	if err := h.Lib.RemoveTag(r.Context(), r.PathValue("id"), r.PathValue("tag")); err != nil {
		httpError(w, err)
		return
	}
	refresh(w, "mediaRefresh", "tagsRefresh")
}

// CreateLink возвращает постоянную ссылку на элемент.
func (h *MediaHandler) CreateLink(w http.ResponseWriter, r *http.Request) {
	url, err := h.Lib.CreateLink(r.Context(), r.PathValue("id"))
	if err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, map[string]string{"url": url})
}

// RevokeLink отзывает постоянную ссылку на элемент.
func (h *MediaHandler) RevokeLink(w http.ResponseWriter, r *http.Request) {
	if err := h.Lib.RevokeLink(r.Context(), r.PathValue("id")); err != nil {
		if errors.Is(err, library.ErrNoLink) {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		httpError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *MediaHandler) Log(w http.ResponseWriter, r *http.Request) {
	log, err := h.Lib.Log(r.Context(), r.PathValue("id"))
	if err != nil || log == "" {
		http.Error(w, "log not available", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write([]byte(log)) //nolint:errcheck
}

func (h *MediaHandler) ListDeleted(w http.ResponseWriter, r *http.Request) {
	items, err := h.Lib.ListDeleted(r.Context())
	if err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, items)
}

// ── Фоновые операции ─────────────────────────────────────────────────────────

func (h *MediaHandler) BulkTag(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TagName string   `json:"tag"`
		JobIDs  []string `json:"job_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.TagName == "" || len(body.JobIDs) == 0 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := h.Lib.EnqueueBulkTag(r.Context(), body.TagName, body.JobIDs); err != nil {
		httpError(w, err)
		return
	}
	accepted(w, "mediaRefresh", "tagsRefresh", "queueRefresh")
}

func (h *MediaHandler) BulkHide(w http.ResponseWriter, r *http.Request) {
	var body struct {
		JobIDs []string `json:"job_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.JobIDs) == 0 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := h.Lib.EnqueueBulkHide(r.Context(), body.JobIDs); err != nil {
		httpError(w, err)
		return
	}
	accepted(w, "mediaRefresh", "queueRefresh")
}

func (h *MediaHandler) UpdateMeta(w http.ResponseWriter, r *http.Request) {
	var meta model.AudioMeta
	if err := json.NewDecoder(r.Body).Decode(&meta); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := h.Lib.EnqueueUpdateMeta(r.Context(), r.PathValue("id"), meta); err != nil {
		httpError(w, err)
		return
	}
	accepted(w, "mediaRefresh", "queueRefresh")
}

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
	if err := h.Lib.EnqueueBulkMeta(r.Context(), req.ItemIDs, req.Fields); err != nil {
		httpError(w, err)
		return
	}
	accepted(w, "mediaRefresh", "queueRefresh")
}

func (h *MediaHandler) ExtractAudio(w http.ResponseWriter, r *http.Request) {
	if err := h.Lib.EnqueueExtractAudio(r.Context(), r.PathValue("id")); err != nil {
		if errors.Is(err, library.ErrNotAvailable) {
			http.Error(w, "item not available", http.StatusGone)
			return
		}
		httpError(w, err)
		return
	}
	trigger := map[string]any{"showToast": "Извлечение аудио запущено", "mediaRefresh": true}
	blob, _ := json.Marshal(trigger)
	w.Header().Set("HX-Trigger", string(blob))
	w.WriteHeader(http.StatusAccepted)
}

// ── общие помощники ──────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, v any) {
	// Пустой слайс в Go — nil, и он сериализуется в null. Клиент ждёт массив
	// и на null падает (например, при разборе списка коллекций).
	if rv := reflect.ValueOf(v); rv.Kind() == reflect.Slice && rv.IsNil() {
		v = []any{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

// refresh отвечает 204 и просит фронтенд обновить перечисленные области.
func refresh(w http.ResponseWriter, events ...string) {
	setTrigger(w, events...)
	w.WriteHeader(http.StatusNoContent)
}

// accepted отвечает 202: работа поставлена в фоновую очередь.
func accepted(w http.ResponseWriter, events ...string) {
	setTrigger(w, events...)
	w.WriteHeader(http.StatusAccepted)
}

func setTrigger(w http.ResponseWriter, events ...string) {
	if len(events) == 0 {
		return
	}
	m := make(map[string]bool, len(events))
	for _, e := range events {
		m[e] = true
	}
	blob, _ := json.Marshal(m)
	w.Header().Set("HX-Trigger", string(blob))
}

func httpError(w http.ResponseWriter, err error) {
	slog.Error("handler", "err", err)
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

func notFoundOrGone(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, library.ErrNotAvailable) {
		http.Error(w, "item not available", http.StatusGone)
		return
	}
	http.NotFound(w, r)
}
