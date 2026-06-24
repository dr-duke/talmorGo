package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/dr-duke/talmorGo/internal/repo"
)

type Enqueuer interface {
	Enqueue()
}

type QueueHandler struct {
	Jobs repo.JobRepo
	Pool Enqueuer
}

// Add добавляет URL(ы) в очередь.
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

	job := &model.Job{
		URL:    rawURL,
		Status: model.JobPending,
		Source: "web",
	}
	if err := h.Jobs.Create(r.Context(), job); err != nil {
		slog.Error("queue add", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	h.Pool.Enqueue()

	// Возвращаем обновлённый media-список (HTMX target = #media-inner).
	w.Header().Set("HX-Trigger", "mediaRefresh")
	w.WriteHeader(http.StatusNoContent)
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
