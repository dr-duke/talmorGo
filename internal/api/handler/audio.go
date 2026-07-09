package handler

import (
	"encoding/json"
	"net/http"

	"github.com/a-h/templ"
	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/dr-duke/talmorGo/internal/repo"
	"github.com/dr-duke/talmorGo/web/templates"
)

type AudioHandler struct {
	Audio repo.AudioRepo
}

// List отдаёт HTMX-фрагмент со списком аудиофайлов.
func (h *AudioHandler) List(w http.ResponseWriter, r *http.Request) {
	files, err := h.Audio.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	templ.Handler(templates.AudioList(files)).ServeHTTP(w, r)
}

// Stream отдаёт аудиофайл (с Range-поддержкой).
func (h *AudioHandler) Stream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	f, err := h.Audio.GetByID(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if r.URL.Query().Get("download") == "true" {
		w.Header().Set("Content-Disposition", `attachment; filename="`+f.Name+`"`)
	}
	http.ServeFile(w, r, f.Path)
}

// UpdateMeta обновляет ID3-метаданные аудиофайла в БД.
func (h *AudioHandler) UpdateMeta(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var meta model.AudioMeta
	if err := json.NewDecoder(r.Body).Decode(&meta); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := h.Audio.UpdateMeta(r.Context(), id, meta); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Trigger", "audioRefresh")
	w.WriteHeader(http.StatusNoContent)
}

// Delete удаляет аудиофайл из БД (файл на диске остаётся — ответственность вызывающего).
func (h *AudioHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.Audio.Delete(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Trigger", "audioRefresh")
	w.WriteHeader(http.StatusNoContent)
}
