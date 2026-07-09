package handler

import (
	"encoding/json"
	"net/http"

	"github.com/a-h/templ"
	"github.com/dr-duke/talmorGo/internal/repo"
	"github.com/dr-duke/talmorGo/web/templates"
)

type CollectionHandler struct {
	Collections repo.CollectionRepo
}

// List отдаёт JSON-список коллекций (для dropdown в action bar).
func (h *CollectionHandler) Fragment(w http.ResponseWriter, r *http.Request) {
	cols, err := h.Collections.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(cols) //nolint:errcheck
}

// Cards отдаёт HTML-фрагмент карточек коллекций (для вкладки Коллекции).
func (h *CollectionHandler) Cards(w http.ResponseWriter, r *http.Request) {
	cols, err := h.Collections.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	templ.Handler(templates.CollectionsCards(cols)).ServeHTTP(w, r)
}

// Create создаёт коллекцию и возвращает JSON с созданной записью.
func (h *CollectionHandler) Create(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	col, err := h.Collections.Create(r.Context(), body.Name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("HX-Trigger", "collectionsRefresh")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(col) //nolint:errcheck
}

// Delete удаляет коллекцию (видео остаются).
func (h *CollectionHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.Collections.Delete(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Trigger", `{"collectionsRefresh":true,"tagsRefresh":true}`)
	w.WriteHeader(http.StatusNoContent)
}

// Rename переименовывает коллекцию.
func (h *CollectionHandler) Rename(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := h.Collections.Rename(r.Context(), id, body.Name); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Trigger", `{"collectionsRefresh":true,"tagsRefresh":true}`)
	w.WriteHeader(http.StatusNoContent)
}

// AddJobs добавляет набор заданий в коллекцию через тег.
func (h *CollectionHandler) AddJobs(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		JobIDs []string `json:"job_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.JobIDs) == 0 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if err := h.Collections.AddJobs(r.Context(), id, body.JobIDs); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("HX-Trigger", `{"collectionsRefresh":true,"tagsRefresh":true,"mediaRefresh":true,"showToast":"Добавлено в коллекцию"}`)
	w.WriteHeader(http.StatusNoContent)
}
