package handler

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/a-h/templ"
	"github.com/dr-duke/talmorGo/internal/model"
	"github.com/dr-duke/talmorGo/internal/repo"
	"github.com/dr-duke/talmorGo/web/templates"
)

type Enqueuer interface {
	Enqueue()
}

type QueueHandler struct {
	Jobs repo.JobRepo
	Pool Enqueuer
}

// Page возвращает полную вкладку очереди (tab switching).
func (h *QueueHandler) Page(w http.ResponseWriter, r *http.Request) {
	jobs, err := h.listJobs(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	templ.Handler(templates.QueueTab(jobs)).ServeHTTP(w, r)
}

// List возвращает только внутренний фрагмент списка (HTMX polling).
func (h *QueueHandler) List(w http.ResponseWriter, r *http.Request) {
	jobs, err := h.listJobs(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	templ.Handler(templates.QueueList(jobs)).ServeHTTP(w, r)
}

func (h *QueueHandler) listJobs(r *http.Request) ([]*model.Job, error) {
	return h.Jobs.List(r.Context(), repo.JobFilter{
		Statuses: []model.JobStatus{
			model.JobPending, model.JobRunning, model.JobRetrying,
			model.JobFailed, model.JobDone,
		},
	})
}

// Add добавляет URL(ы) в очередь. Принимает form или JSON: {"url": "..."}.
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
	h.List(w, r)
}

// Delete отменяет pending-задачу.
func (h *QueueHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.Jobs.Delete(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.List(w, r)
}

// Retry переводит failed-задачу обратно в pending для повторного скачивания.
func (h *QueueHandler) Retry(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := h.Jobs.ResetFailed(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	h.Pool.Enqueue()
	h.List(w, r)
}
