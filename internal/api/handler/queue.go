package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/a-h/templ"
	"github.com/dr-duke/talmorGo/internal/library"
	"github.com/dr-duke/talmorGo/internal/queue"
	"github.com/dr-duke/talmorGo/web/templates"
)

// QueueHandler — адаптер HTTP → queue.Service.
type QueueHandler struct {
	Queue *queue.Service
	Lib   *library.Service
}

// Add ставит ссылку в очередь. Ответ уходит сразу: разворачивание плейлиста
// идёт в фоне.
func (h *QueueHandler) Add(w http.ResponseWriter, r *http.Request) {
	rawURL := ""
	if r.Header.Get("Content-Type") == "application/json" {
		var body struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		rawURL = body.URL
	} else {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		rawURL = r.FormValue("url")
	}

	if rawURL == "" {
		http.Error(w, "url required", http.StatusBadRequest)
		return
	}

	if _, err := h.Queue.Add(r.Context(), rawURL, "web", 0); err != nil {
		if errors.Is(err, queue.ErrInvalidURL) {
			http.Error(w, "invalid url", http.StatusBadRequest)
			return
		}
		httpError(w, err)
		return
	}
	refresh(w, "mediaRefresh", "queueRefresh")
}

// Delete отменяет задание в любом статусе.
func (h *QueueHandler) Delete(w http.ResponseWriter, r *http.Request) {
	if err := h.Queue.Cancel(r.Context(), r.PathValue("id")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	refresh(w, "mediaRefresh", "queueRefresh")
}

// CancelAll отменяет все активные задания, включая идущие загрузки.
func (h *QueueHandler) CancelAll(w http.ResponseWriter, r *http.Request) {
	if _, err := h.Queue.CancelAll(r.Context()); err != nil {
		httpError(w, err)
		return
	}
	refresh(w, "mediaRefresh", "queueRefresh")
}

// Items отдаёт фрагмент очереди: фоновые операции + задания.
func (h *QueueHandler) Items(w http.ResponseWriter, r *http.Request) {
	jobs, err := h.Queue.List(r.Context())
	if err != nil {
		httpError(w, err)
		return
	}
	operations, err := h.Lib.Operations(r.Context())
	if err != nil {
		operations = nil
	}
	templ.Handler(templates.QueueItems(jobs, operations)).ServeHTTP(w, r)
}

func (h *QueueHandler) Retry(w http.ResponseWriter, r *http.Request) {
	if err := h.Queue.Retry(r.Context(), r.PathValue("id")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	refresh(w, "mediaRefresh", "queueRefresh")
}

func (h *QueueHandler) Redownload(w http.ResponseWriter, r *http.Request) {
	if err := h.Queue.Redownload(r.Context(), r.PathValue("id")); err != nil {
		httpError(w, err)
		return
	}
	refresh(w, "mediaRefresh", "queueRefresh")
}

// DismissOp убирает завершённую операцию из очереди.
func (h *QueueHandler) DismissOp(w http.ResponseWriter, r *http.Request) {
	if err := h.Lib.DismissOperation(r.Context(), r.PathValue("id")); err != nil {
		httpError(w, err)
		return
	}
	refresh(w, "mediaRefresh", "queueRefresh")
}

// CancelOp прерывает выполняющуюся операцию.
func (h *QueueHandler) CancelOp(w http.ResponseWriter, r *http.Request) {
	if err := h.Lib.CancelOperation(r.Context(), r.PathValue("id")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	refresh(w, "mediaRefresh", "queueRefresh")
}
