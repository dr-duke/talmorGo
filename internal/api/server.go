package api

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/a-h/templ"
	"github.com/dr-duke/talmorGo/internal/api/handler"
	"github.com/dr-duke/talmorGo/internal/config"
	"github.com/dr-duke/talmorGo/internal/library"
	"github.com/dr-duke/talmorGo/internal/queue"
	"github.com/dr-duke/talmorGo/internal/repo"
	"github.com/dr-duke/talmorGo/internal/settings"
	"github.com/dr-duke/talmorGo/internal/sse"
	"github.com/dr-duke/talmorGo/web"
	"github.com/dr-duke/talmorGo/web/templates"
)

type Server struct {
	cfg     *config.Config
	handler http.Handler
}

// Deps — всё, что нужно HTTP-слою. Доменная логика живёт в сервисах,
// репозитории сюда попадают только там, где адаптер и есть вся работа (куки).
type Deps struct {
	Cfg      *config.Config
	Lib      *library.Service
	Queue    *queue.Service
	Settings *settings.Provider
	Cookies  repo.CookieRepo
	Hub      *sse.Hub
}

func New(d Deps) *Server {
	cfg := d.Cfg
	basePath := strings.TrimRight(cfg.BasePath, "/")

	mux := http.NewServeMux()

	mh := &handler.MediaHandler{Lib: d.Lib, Cfg: cfg}
	qh := &handler.QueueHandler{Queue: d.Queue, Lib: d.Lib}
	ch := &handler.CollectionHandler{Lib: d.Lib}
	lh := &handler.LinkHandler{Lib: d.Lib}
	sh := &handler.SettingsHandler{
		Cookies: d.Cookies, Settings: d.Settings, Lib: d.Lib,
		Cfg: cfg, SiteName: cfg.SiteName,
	}
	ah := &handler.AuthHandler{Token: cfg.WebToken, BasePath: basePath, SiteName: cfg.SiteName}

	// Статика: с ETag и обязательной перепроверкой, иначе браузер держит
	// устаревшие стили и скрипты (см. web.StaticHandler).
	mux.Handle("GET /static/", http.StripPrefix("/static/", web.StaticHandler()))

	// Главная страница.
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		cols, _ := d.Lib.ListCollections(r.Context())
		templ.Handler(templates.Index(basePath, cfg.SiteName, cols)).ServeHTTP(w, r)
	})

	// Вход по токену.
	if cfg.WebToken != "" {
		mux.HandleFunc("GET /login", ah.LoginPage)
		mux.HandleFunc("POST /login", ah.Login)
		mux.HandleFunc("POST /logout", ah.Logout)
	}

	// Медиатека.
	mux.HandleFunc("GET /library/sidebar", mh.LibrarySidebar)
	mux.HandleFunc("GET /library/items", mh.LibraryItems)
	mux.HandleFunc("GET /library/tags", mh.TagsFragment)
	mux.HandleFunc("GET /library/playlist", mh.PlaylistItems)

	// Элементы.
	mux.HandleFunc("GET /items/{id}/stream", mh.Stream)
	mux.HandleFunc("DELETE /items/{id}", mh.Delete)
	mux.HandleFunc("PATCH /items/{id}", mh.Rename)
	mux.HandleFunc("PATCH /items/{id}/meta", mh.UpdateMeta)
	mux.HandleFunc("POST /items/meta-bulk", mh.BulkMeta)
	mux.HandleFunc("POST /items/{id}/link", mh.CreateLink)
	mux.HandleFunc("DELETE /items/{id}/link", mh.RevokeLink)
	mux.HandleFunc("POST /items/{id}/extract-audio", mh.ExtractAudio)
	mux.HandleFunc("POST /items/extract-audio-bulk", mh.ExtractAudioBulk)
	mux.HandleFunc("GET /items/deleted", mh.ListDeleted)

	// Задания.
	mux.HandleFunc("POST /jobs/{id}/redownload", qh.Redownload)
	mux.HandleFunc("POST /jobs/{id}/hide", mh.Hide)
	mux.HandleFunc("POST /jobs/{id}/unhide", mh.Unhide)
	mux.HandleFunc("DELETE /jobs/{id}", mh.PurgeJob)
	mux.HandleFunc("POST /jobs/{id}/tags", mh.AddTag)
	mux.HandleFunc("DELETE /jobs/{id}/tags/{tag}", mh.RemoveTag)
	mux.HandleFunc("GET /jobs/{id}/log", mh.Log)
	mux.HandleFunc("POST /media/bulk-tag", mh.BulkTag)
	mux.HandleFunc("POST /media/bulk-hide", mh.BulkHide)

	// Коллекции.
	mux.HandleFunc("GET /collections", ch.ListJSON)
	mux.HandleFunc("POST /collections", ch.Create)
	mux.HandleFunc("PATCH /collections/{id}", ch.Rename)
	mux.HandleFunc("DELETE /collections/{id}", ch.Delete)
	mux.HandleFunc("POST /collections/{id}/jobs", ch.AddJobs)

	// Очередь.
	mux.HandleFunc("POST /queue", qh.Add)
	mux.HandleFunc("DELETE /queue/{id}", qh.Delete)
	mux.HandleFunc("POST /queue/cancel-all", qh.CancelAll)
	mux.HandleFunc("GET /queue/items", qh.Items)
	mux.HandleFunc("POST /jobs/{id}/retry", qh.Retry)
	mux.HandleFunc("DELETE /operations/{id}", qh.DismissOp)
	mux.HandleFunc("POST /operations/{id}/cancel", qh.CancelOp)

	// Настройки.
	mux.HandleFunc("GET /settings", sh.Page)
	mux.HandleFunc("POST /settings/cookies/import", sh.Import)
	mux.HandleFunc("DELETE /settings/cookies/{domain}", sh.DeleteDomain)
	mux.HandleFunc("POST /settings/cleanup", sh.Cleanup)
	mux.HandleFunc("POST /settings/reindex", sh.Reindex)
	mux.HandleFunc("POST /settings/runtime", sh.SaveRuntimeSettings)

	// SSE.
	mux.HandleFunc("GET /events", eventsHandler(d.Hub))

	// Постоянная ссылка (публичная).
	mux.HandleFunc("GET /f/{token}", lh.Resolve)

	// Health.
	if cfg.HealthEndpoint != "" {
		mux.HandleFunc("GET "+cfg.HealthEndpoint, handler.Health)
	}

	var h http.Handler = mux
	if cfg.WebToken != "" {
		h = authMiddleware(cfg.WebToken, cfg.HealthEndpoint, basePath, mux)
	}

	if basePath != "" {
		outer := http.NewServeMux()
		if cfg.HealthEndpoint != "" {
			outer.HandleFunc("GET "+cfg.HealthEndpoint, handler.Health)
		}
		outer.HandleFunc("GET "+basePath, func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, basePath+"/", http.StatusMovedPermanently)
		})
		outer.Handle(basePath+"/", http.StripPrefix(basePath, h))
		h = outer
	}

	return &Server{cfg: cfg, handler: h}
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

func eventsHandler(hub *sse.Hub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		ch, unsub := hub.Subscribe()
		defer unsub()

		fmt.Fprint(w, "event: ping\ndata: ok\n\n")
		flusher.Flush()

		for {
			select {
			case <-r.Context().Done():
				return
			case topics, ok := <-ch:
				if !ok {
					return
				}
				// Каждая тема — отдельное событие: браузер обновляет только её.
				for _, t := range topics {
					fmt.Fprintf(w, "event: %s\ndata: 1\n\n", t)
				}
				flusher.Flush()
			}
		}
	}
}

// publicPath — маршруты, доступные без авторизации: постоянные ссылки,
// страница входа и статика, без которой эта страница не отрисуется.
func publicPath(path, healthEndpoint string) bool {
	switch {
	case strings.HasPrefix(path, "/f/"),
		strings.HasPrefix(path, "/static/"),
		path == "/login":
		return true
	case healthEndpoint != "" && path == healthEndpoint:
		return true
	}
	return false
}

// authMiddleware закрывает всё, кроме публичных маршрутов.
// basePath нужен для редиректа: внутрь обработчика запрос приходит уже без
// префикса, поэтому относительный «login» увёл бы за точку монтирования.
func authMiddleware(token, healthEndpoint, basePath string, next http.Handler) http.Handler {
	loginURL := basePath + "/login"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if publicPath(r.URL.Path, healthEndpoint) {
			next.ServeHTTP(w, r)
			return
		}
		if r.Header.Get("Authorization") == "Bearer "+token {
			next.ServeHTTP(w, r)
			return
		}
		if c, err := r.Cookie(handler.AuthCookie); err == nil && c.Value == token {
			next.ServeHTTP(w, r)
			return
		}
		// Обычную навигацию отправляем на форму входа, запросы данных — 401.
		if r.Method == http.MethodGet && strings.Contains(r.Header.Get("Accept"), "text/html") {
			http.Redirect(w, r, loginURL, http.StatusSeeOther)
			return
		}
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
	})
}
