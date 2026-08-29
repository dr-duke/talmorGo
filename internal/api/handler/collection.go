package handler

import (
	"encoding/json"
	"net/http"

	"github.com/dr-duke/talmorGo/internal/library"
)

type CollectionHandler struct {
	Lib *library.Service
}

// ListJSON отдаёт коллекции для выпадающего списка в панели действий.
func (h *CollectionHandler) ListJSON(w http.ResponseWriter, r *http.Request) {
	cols, err := h.Lib.ListCollections(r.Context())
	if err != nil {
		httpError(w, err)
		return
	}
	writeJSON(w, cols)
}

func (h *CollectionHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	col, err := h.Lib.CreateCollection(r.Context(), body.Name)
	if err != nil {
		httpError(w, err)
		return
	}
	setTrigger(w, "collectionsRefresh")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(col) //nolint:errcheck
}

// Delete удаляет коллекцию; сами видео остаются в медиатеке.
func (h *CollectionHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.Lib.DeleteCollection(r.Context(), r.PathValue("id")); err != nil {
		httpError(w, err)
		return
	}
	refresh(w, "collectionsRefresh", "tagsRefresh", "mediaRefresh")
}

func (h *CollectionHandler) Rename(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := h.Lib.RenameCollection(r.Context(), r.PathValue("id"), body.Name); err != nil {
		httpError(w, err)
		return
	}
	refresh(w, "collectionsRefresh", "tagsRefresh", "mediaRefresh")
}

// AddJobs добавляет выбранные задания в коллекцию.
func (h *CollectionHandler) AddJobs(w http.ResponseWriter, r *http.Request) {
	var body struct {
		JobIDs []string `json:"job_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.JobIDs) == 0 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := h.Lib.AddJobsToCollection(r.Context(), r.PathValue("id"), body.JobIDs); err != nil {
		httpError(w, err)
		return
	}
	refresh(w, "collectionsRefresh", "tagsRefresh", "mediaRefresh")
}
