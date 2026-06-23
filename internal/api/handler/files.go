package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"path/filepath"

	"github.com/a-h/templ"
	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/dr-duke/talmorGo/internal/repo"
	"github.com/dr-duke/talmorGo/internal/storage"
	"github.com/dr-duke/talmorGo/web/templates"
)

type FilesHandler struct {
	Files   repo.FileRepo
	Tokens  repo.TokenRepo
	Storage *storage.Storage
	BaseURL string
}

// Page возвращает полную вкладку файлов (tab switching).
func (h *FilesHandler) Page(w http.ResponseWriter, r *http.Request) {
	files, err := h.Files.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	templ.Handler(templates.FilesTab(files)).ServeHTTP(w, r)
}

// List возвращает только внутренний фрагмент списка файлов (HTMX polling).
func (h *FilesHandler) List(w http.ResponseWriter, r *http.Request) {
	files, err := h.Files.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	templ.Handler(templates.FilesList(files)).ServeHTTP(w, r)
}

// Stream отдаёт файл с поддержкой Range-запросов (для видеоплеера и скачивания).
func (h *FilesHandler) Stream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	f, err := h.Files.GetByID(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if r.URL.Query().Get("download") == "true" {
		w.Header().Set("Content-Disposition", `attachment; filename="`+filepath.Base(f.Name)+`"`)
	}
	http.ServeFile(w, r, f.Path)
}

// Delete удаляет файл с диска и из БД.
func (h *FilesHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	f, err := h.Files.GetByID(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := h.Storage.Delete(f.Path); err != nil {
		slog.Error("files delete: storage", "err", err)
	}
	if err := h.Files.Delete(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.List(w, r)
}

// Rename переименовывает файл на диске и в БД.
func (h *FilesHandler) Rename(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Name string `json:"name"`
	}
	ct := r.Header.Get("Content-Type")
	if ct == "application/json" {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
	} else {
		r.ParseForm()
		body.Name = r.FormValue("name")
	}
	if body.Name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}

	f, err := h.Files.GetByID(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	newPath, err := h.Storage.Rename(f.Path, body.Name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Обновляем путь и имя в БД.
	f.Path = newPath
	f.Name = body.Name
	if err := h.Files.Rename(r.Context(), id, body.Name, newPath); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	h.List(w, r)
}

// ListDeleted возвращает JSON-список удалённых файлов с исходным URL.
func (h *FilesHandler) ListDeleted(w http.ResponseWriter, r *http.Request) {
	files, err := h.Files.ListDeleted(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if files == nil {
		files = []*model.DeletedFile{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(files)
}

// CreateLink создаёт или возвращает presigned-ссылку для файла.
func (h *FilesHandler) CreateLink(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, err := h.Files.GetByID(r.Context(), id); err != nil {
		http.NotFound(w, r)
		return
	}
	token, err := h.Tokens.Upsert(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	linkURL := h.BaseURL + "/f/" + token.Token
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"url": linkURL})
}
